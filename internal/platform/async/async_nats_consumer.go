package async

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nats-io/nats.go"

	"github.com/admin-frame/admin-frame/pkg/mq"
)

type natsConsumer struct {
	broker    *natsBroker
	mu        sync.Mutex
	handlers  map[string][]mq.Handler
	subs      []*nats.Subscription
	sem       chan struct{}
	runCtx    context.Context
	wg        sync.WaitGroup
	done      chan struct{}
	closeOnce sync.Once
	closed    atomic.Bool
}

func newNATSConsumer(b *natsBroker) *natsConsumer {
	return &natsConsumer{
		broker:   b,
		handlers: make(map[string][]mq.Handler),
		sem:      make(chan struct{}, b.opts.MaxAckPending),
		done:     make(chan struct{}),
	}
}

// Subscribe 注册 topic handler；同一 topic 可注册多个（本地扇出）。
func (c *natsConsumer) Subscribe(topic string, h mq.Handler) error {
	topic = strings.TrimSpace(topic)
	if err := validateNATSTopic(topic); err != nil {
		return err
	}
	if h == nil {
		return fmt.Errorf("%w: handler is required", mq.ErrInvalidConfig)
	}
	if c.closed.Load() {
		return mq.ErrClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[topic] = append(c.handlers[topic], h)
	return nil
}

// Run 为每个已注册 topic 建立持久订阅，阻塞至 ctx 取消后优雅收尾。
func (c *natsConsumer) Run(ctx context.Context) error {
	if c.closed.Load() {
		return mq.ErrClosed
	}
	b := c.broker
	c.mu.Lock()
	topics := make([]string, 0, len(c.handlers))
	for topic := range c.handlers {
		topics = append(topics, topic)
	}
	c.runCtx = ctx
	c.mu.Unlock()
	sort.Strings(topics)
	if len(topics) == 0 {
		return fmt.Errorf("%w: no subscriptions", mq.ErrInvalidConfig)
	}
	if err := b.ensureStream(ctx); err != nil {
		return err
	}
	for _, topic := range topics {
		name := durableName(b.opts.Durable, topic)
		sub, err := b.js.QueueSubscribe(b.subject(topic), name, c.handleMessage,
			nats.ManualAck(),
			nats.AckExplicit(),
			nats.AckWait(b.opts.AckWait),
			nats.MaxDeliver(b.opts.MaxDeliver),
			nats.Durable(name),
			nats.BindStream(b.opts.Stream),
			nats.DeliverAll(),
			nats.MaxAckPending(b.opts.MaxAckPending),
		)
		if err != nil {
			c.unsubscribeAll()
			return fmt.Errorf("nats consumer: subscribe %s: %w", topic, err)
		}
		c.mu.Lock()
		c.subs = append(c.subs, sub)
		c.mu.Unlock()
	}
	select {
	case <-ctx.Done():
	case <-c.done:
	}
	c.unsubscribeAll()
	c.wg.Wait()
	return nil
}

// Close 停止消费并释放订阅；底层连接由 Broker.Close 负责。
func (c *natsConsumer) Close() error {
	if c == nil || c.closed.Swap(true) {
		return nil
	}
	c.closeOnce.Do(func() { close(c.done) })
	c.unsubscribeAll()
	return nil
}

func (c *natsConsumer) unsubscribeAll() {
	c.mu.Lock()
	subs := c.subs
	c.subs = nil
	c.mu.Unlock()
	for _, sub := range subs {
		if err := sub.Drain(); err != nil && !errors.Is(err, nats.ErrBadSubscription) {
			c.log().Warn("nats consumer: drain subscription", "err", err)
		}
	}
}

// handleMessage 把 NATS 消息还原为 mq.Message 并分发给本地 handler。
func (c *natsConsumer) handleMessage(nmsg *nats.Msg) {
	ctx := c.context()
	if ctx == nil {
		return
	}
	topic := strings.TrimPrefix(nmsg.Subject, c.broker.opts.SubjectPrefix+".")
	if topic == nmsg.Subject || topic == "" {
		c.log().Warn("nats consumer: unexpected subject", "subject", nmsg.Subject)
		_ = nmsg.Ack()
		return
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer func() { <-c.sem }()
		c.dispatch(ctx, topic, nmsg)
	}()
}

func (c *natsConsumer) dispatch(ctx context.Context, topic string, nmsg *nats.Msg) {
	msg := messageFromNATS(topic, nmsg)
	handlers := c.handlersFor(topic)
	if len(handlers) == 0 {
		_ = nmsg.Ack()
		c.observeConsume(ctx, "ack")
		return
	}
	var firstErr error
	for _, h := range handlers {
		if err := invokeHandler(ctx, h, msg); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		_ = nmsg.Ack()
		c.observeConsume(ctx, "ack")
		return
	}
	if c.deliveries(nmsg) >= c.broker.opts.MaxDeliver {
		c.deadLetter(ctx, topic, nmsg, firstErr)
		return
	}
	c.observeConsume(ctx, "retry")
	if err := nmsg.NakWithDelay(natsRetryDelay); err != nil {
		c.log().Warn("nats consumer: nak", "topic", topic, "err", err)
	}
}

// deadLetter 把毒消息转入 <prefix>.dlq.<topic> 后 Ack 原消息。
//
// DLQ 消息必须使用独立的消息 ID：同一流内 JetStream 按 Nats-Msg-Id 去重，
// 直接复制原消息头会让死信被当成重复消息丢弃。
func (c *natsConsumer) deadLetter(ctx context.Context, topic string, nmsg *nats.Msg, cause error) {
	header := copyHeader(nmsg.Header)
	delete(header, nats.MsgIdHdr)
	header.Set("mq.dlq_cause", truncateError(cause))
	header.Set("mq.dlq_source_subject", nmsg.Subject)

	opts := []nats.PubOpt{nats.Context(ctx)}
	if meta, err := nmsg.Metadata(); err == nil {
		opts = append(opts, nats.MsgId(fmt.Sprintf("dlq-%d", meta.Sequence.Stream)))
	}
	dlq := &nats.Msg{
		Subject: c.broker.dlqSubject(topic),
		Header:  header,
		Data:    nmsg.Data,
	}
	if _, err := c.broker.js.PublishMsg(dlq, opts...); err != nil {
		c.log().Warn("nats consumer: dead letter publish", "topic", topic, "err", err)
		if nakErr := nmsg.NakWithDelay(natsRetryDelay); nakErr != nil {
			c.log().Warn("nats consumer: nak after dlq failure", "topic", topic, "err", nakErr)
		}
		return
	}
	_ = nmsg.Ack()
	c.observeConsume(ctx, "dead")
}

func (c *natsConsumer) handlersFor(topic string) []mq.Handler {
	c.mu.Lock()
	defer c.mu.Unlock()
	src := c.handlers[topic]
	if len(src) == 0 {
		return nil
	}
	out := make([]mq.Handler, len(src))
	copy(out, src)
	return out
}

func (c *natsConsumer) context() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runCtx
}

func (c *natsConsumer) deliveries(nmsg *nats.Msg) int {
	meta, err := nmsg.Metadata()
	if err != nil {
		return 1
	}
	return int(meta.NumDelivered) //nolint:gosec // 投递次数远小于 int 上限，比较阈值为 MaxDeliver。
}

func (c *natsConsumer) observeConsume(ctx context.Context, result string) {
	if c.broker.opts.Observer != nil {
		c.broker.opts.Observer.OnBrokerConsume(ctx, result)
	}
}

func (c *natsConsumer) log() *slog.Logger {
	if c.broker.logger != nil {
		return c.broker.logger
	}
	return slog.Default()
}

// invokeHandler 捕获 panic，把 panic 视为不可重试错误。

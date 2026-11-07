package async

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform/config"

	"github.com/nats-io/nats.go"

	"github.com/admin-frame/admin-frame/pkg/mq"
)

// 适配器内部的固定参数；随容量诉求再开放为配置项。
const (
	natsEventRetention  = 24 * time.Hour
	natsDuplicateWindow = 2 * time.Minute
	natsRetryDelay      = time.Second
	natsPublishTimeout  = 5 * time.Second
)

// BrokerObserver 观测 broker 发布/消费结果；result 取值见实现注释。
type BrokerObserver interface {
	// OnBrokerPublish result: success | error。
	OnBrokerPublish(ctx context.Context, result string)
	// OnBrokerConsume result: ack | retry | dead。
	OnBrokerConsume(ctx context.Context, result string)
}

// NATSBrokerOptions 是 JetStream Broker 的装配参数。
type NATSBrokerOptions struct {
	// URL 必填，支持 nats:// / tls:// / ws:// / wss://。
	URL string
	// Stream 流名称。
	Stream string
	// SubjectPrefix 主题前缀；最终 subject 为 <prefix>.<topic>。
	SubjectPrefix string
	// Durable 消费端持久名；同一服务多副本共享，不同服务互相独立。
	Durable string
	// AckWait 确认超时。
	AckWait time.Duration
	// MaxDeliver 最大投递次数；超过进入 <prefix>.dlq.<topic>。
	MaxDeliver int
	// MaxAckPending 单订阅在途上限；≤0 取 16。
	MaxAckPending int
	// Logger 可为 nil。
	Logger *slog.Logger
	// Observer 可为 nil。
	Observer BrokerObserver
}

// NewNATSBroker 连接 NATS 并返回实现 mq.Broker 的适配器。
//
// 事务性 Outbox 仍是事实源：调用方应把 Outbox 事件发布到 broker，
// 而不是在业务事务中直接发布（避免双写不一致）。
func NewNATSBroker(opts NATSBrokerOptions) (mq.Broker, error) {
	url := strings.TrimSpace(opts.URL)
	if url == "" {
		return nil, fmt.Errorf("nats broker: url is required")
	}
	if !config.ValidNATSStreamName(opts.Stream) {
		return nil, fmt.Errorf("nats broker: invalid stream %q", opts.Stream)
	}
	if !config.ValidNATSSubjectRoot(opts.SubjectPrefix) {
		return nil, fmt.Errorf("nats broker: invalid subject_prefix %q", opts.SubjectPrefix)
	}
	durable := strings.TrimSpace(opts.Durable)
	if durable == "" {
		return nil, fmt.Errorf("nats broker: durable name is required")
	}
	if opts.AckWait <= 0 {
		opts.AckWait = 30 * time.Second
	}
	if opts.MaxDeliver <= 0 {
		opts.MaxDeliver = 10
	}
	if opts.MaxAckPending <= 0 {
		opts.MaxAckPending = 16
	}

	conn, err := nats.Connect(url,
		nats.Name("admin-frame/"+durable),
		nats.Timeout(natsPublishTimeout),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.PingInterval(20*time.Second),
		nats.MaxPingsOutstanding(2),
	)
	if err != nil {
		return nil, fmt.Errorf("nats broker: connect: %w", err)
	}
	js, err := conn.JetStream(nats.MaxWait(natsPublishTimeout))
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("nats broker: jetstream: %w", err)
	}
	return &natsBroker{
		opts:     opts,
		conn:     conn,
		js:       js,
		producer: &natsProducer{broker: nil},
		logger:   opts.Logger,
	}, nil
}

// natsBroker 同时提供 Producer 与 Consumer；Producer/Consumer 共享同一连接。
type natsBroker struct {
	opts     NATSBrokerOptions
	conn     *nats.Conn
	js       nats.JetStreamContext
	producer *natsProducer
	logger   *slog.Logger

	mu       sync.Mutex
	consumer *natsConsumer
	closed   atomic.Bool
}

// Producer 返回发布端。
func (b *natsBroker) Producer() mq.Producer {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.producer.broker == nil {
		b.producer.broker = b
	}
	return b.producer
}

// Consumer 返回消费端；同一 Broker 只允许一个消费实例。
func (b *natsBroker) Consumer() mq.Consumer {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.consumer == nil {
		b.consumer = newNATSConsumer(b)
	}
	return b.consumer
}

// Close 关闭连接；Producer/Consumer 的 Close 复用同一实现。
func (b *natsBroker) Close() error {
	if b == nil || b.closed.Swap(true) {
		return nil
	}
	if err := b.conn.Drain(); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
		b.conn.Close()
		return fmt.Errorf("nats broker: drain: %w", err)
	}
	return nil
}

// subject 返回 <prefix>.<topic>。
func (b *natsBroker) subject(topic string) string {
	return b.opts.SubjectPrefix + "." + topic
}

// dlqSubject 返回 <prefix>.dlq.<topic>。
func (b *natsBroker) dlqSubject(topic string) string {
	return b.opts.SubjectPrefix + ".dlq." + topic
}

// ensureStream 幂等创建/更新流，订阅前缀下的全部 subject（含 dlq）。
func (b *natsBroker) ensureStream(ctx context.Context) error {
	wildcard := b.opts.SubjectPrefix + ".>"
	info, err := b.js.StreamInfo(b.opts.Stream)
	switch {
	case errors.Is(err, nats.ErrStreamNotFound):
		_, err = b.js.AddStream(&nats.StreamConfig{
			Name:       b.opts.Stream,
			Subjects:   []string{wildcard},
			Retention:  nats.LimitsPolicy,
			Storage:    nats.FileStorage,
			MaxAge:     natsEventRetention,
			Duplicates: natsDuplicateWindow,
			Discard:    nats.DiscardOld,
		}, nats.Context(ctx))
		if err != nil {
			return fmt.Errorf("nats broker: add stream %q: %w", b.opts.Stream, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("nats broker: stream info %q: %w", b.opts.Stream, err)
	}
	cfg := info.Config
	for _, subject := range cfg.Subjects {
		if subject == wildcard {
			return nil
		}
	}
	cfg.Subjects = append(cfg.Subjects, wildcard)
	if _, err := b.js.UpdateStream(&cfg, nats.Context(ctx)); err != nil {
		return fmt.Errorf("nats broker: update stream %q: %w", b.opts.Stream, err)
	}
	return nil
}

// observePublish 上报发布结果；Observer 为空时静默。
func (b *natsBroker) observePublish(ctx context.Context, result string) {
	if b.opts.Observer != nil {
		b.opts.Observer.OnBrokerPublish(ctx, result)
	}
}

// natsProducer 是 mq.Producer 实现。
type natsProducer struct {
	broker *natsBroker
}

// Publish 发布消息；至少一次语义由 JetStream 与 MsgId 去重窗口共同保证。
func (p *natsProducer) Publish(ctx context.Context, msg mq.Message) error {
	b := p.broker
	if b == nil {
		return fmt.Errorf("nats producer: broker is not attached")
	}
	if b.closed.Load() {
		return mq.ErrClosed
	}
	if err := msg.Validate(); err != nil {
		return err
	}
	topic := strings.TrimSpace(msg.Topic)
	if err := validateNATSTopic(topic); err != nil {
		return err
	}
	msg = prepareNATSMessage(msg)
	if err := b.ensureStream(ctx); err != nil {
		b.observePublish(ctx, "error")
		return err
	}
	_, err := b.js.PublishMsg(&nats.Msg{
		Subject: b.subject(topic),
		Header:  toNATSHeader(msg.Headers),
		Data:    msg.Body,
	}, nats.MsgId(msg.ID), nats.Context(ctx))
	if err != nil {
		b.observePublish(ctx, "error")
		return fmt.Errorf("nats producer: publish %s: %w", topic, err)
	}
	b.observePublish(ctx, "success")
	return nil
}

// Close 关闭底层连接。
func (p *natsProducer) Close() error {
	if p.broker == nil {
		return nil
	}
	return p.broker.Close()
}

// natsConsumer 是 mq.Consumer 实现；订阅按 topic 扇出到本地 handler。
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
func invokeHandler(ctx context.Context, h mq.Handler, msg mq.Message) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", mq.ErrHandlerPanic, r)
		}
	}()
	return h(ctx, msg)
}

// messageFromNATS 还原 mq.Message；topic 来自订阅 subject 去前缀。
func messageFromNATS(topic string, nmsg *nats.Msg) mq.Message {
	headers := make(map[string]string, len(nmsg.Header))
	for key, values := range nmsg.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	attempt := 1
	if raw := headers[mq.HeaderAttempt]; raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			attempt = parsed
		}
	}
	msg := mq.Message{
		ID:        headers[mq.HeaderMessageID],
		Topic:     topic,
		Headers:   headers,
		Body:      append([]byte(nil), nmsg.Data...),
		Timestamp: time.Now().UTC(),
		Attempt:   attempt,
	}
	if meta, err := nmsg.Metadata(); err == nil {
		msg.Attempt = int(meta.NumDelivered) //nolint:gosec // 投递次数远小于 int 上限，仅用于展示。
		if msg.ID == "" {
			msg.ID = meta.Stream + ":" + strconv.FormatUint(meta.Sequence.Stream, 10)
		}
		if !meta.Timestamp.IsZero() {
			msg.Timestamp = meta.Timestamp.UTC()
		}
	}
	msg.Headers = mq.HeaderSet(msg.Headers, mq.HeaderAttempt, strconv.Itoa(msg.Attempt))
	return msg
}

// prepareNATSMessage 补齐 ID/时间/attempt，与 pkg/mq 内存实现保持一致。
func prepareNATSMessage(msg mq.Message) mq.Message {
	out := msg.Clone()
	if out.ID == "" {
		out.ID = newNATSMessageID()
	}
	if out.Timestamp.IsZero() {
		out.Timestamp = time.Now().UTC()
	}
	if out.Attempt < 1 {
		out.Attempt = 1
	}
	out.Headers = mq.HeaderSet(out.Headers, mq.HeaderMessageID, out.ID)
	out.Headers = mq.HeaderSet(out.Headers, mq.HeaderAttempt, strconv.Itoa(out.Attempt))
	return out
}

func newNATSMessageID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(b[:])
}

// validateNATSTopic 校验业务 topic：不含空白与通配符。
func validateNATSTopic(topic string) error {
	if topic == "" {
		return fmt.Errorf("%w: topic is required", mq.ErrInvalidMessage)
	}
	if strings.ContainsAny(topic, " \t\r\n*>") {
		return fmt.Errorf("%w: topic %q contains invalid characters", mq.ErrInvalidMessage, topic)
	}
	return nil
}

// durableName 生成 JetStream durable/queue 名称：仅保留 ASCII 字母数字，其余替换为下划线。
func durableName(durable, topic string) string {
	raw := durable + "_" + strings.ReplaceAll(topic, ".", "_")
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// toNATSHeader 把 mq 的单值头转换为 NATS 的多值头。
func toNATSHeader(src map[string]string) nats.Header {
	if len(src) == 0 {
		return nil
	}
	out := make(nats.Header, len(src))
	for key, value := range src {
		out[key] = []string{value}
	}
	return out
}

func copyHeader(src nats.Header) nats.Header {
	out := make(nats.Header, len(src))
	for key, values := range src {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func truncateError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	const maxErrorLen = 512
	if len(msg) > maxErrorLen {
		return msg[:maxErrorLen]
	}
	return msg
}

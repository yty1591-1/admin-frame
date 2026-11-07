package async

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

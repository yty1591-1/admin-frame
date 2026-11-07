package platform_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	platformasync "github.com/admin-frame/admin-frame/internal/platform/async"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/pkg/mq"
)

// startJetStream 启动一次性嵌入式 NATS Server（开启 JetStream）。
func startJetStream(t *testing.T) string {
	t.Helper()
	srv, err := natsserver.NewServer(&natsserver.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	})
	if err != nil {
		t.Fatalf("nats server: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats server not ready for connections")
	}
	t.Cleanup(srv.Shutdown)
	return srv.ClientURL()
}

// testNATSBroker 用嵌入式服务装配 Broker，结束后关闭连接；同时返回服务地址。
func testNATSBroker(t *testing.T, opts platformasync.NATSBrokerOptions) (broker mq.Broker, url string) {
	t.Helper()
	opts.URL = startJetStream(t)
	opts.Stream = "async"
	opts.SubjectPrefix = "async"
	opts.Durable = "test"
	if opts.AckWait <= 0 {
		opts.AckWait = 5 * time.Second
	}
	broker, err := platformasync.NewNATSBroker(opts)
	if err != nil {
		t.Fatalf("new nats broker: %v", err)
	}
	t.Cleanup(func() {
		if err := broker.Close(); err != nil {
			t.Logf("close broker: %v", err)
		}
	})
	return broker, opts.URL
}

// runConsumer 在后台启动消费，返回等待优雅退出的停止函数。
func runConsumer(t *testing.T, consumer mq.Consumer) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- consumer.Run(ctx) }()
	return func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("consumer run: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("consumer did not stop")
		}
	}
}

func TestNATSBrokerPublishConsume(t *testing.T) {
	broker, _ := testNATSBroker(t, platformasync.NATSBrokerOptions{MaxDeliver: 3, MaxAckPending: 4})

	received := make(chan mq.Message, 1)
	consumer := broker.Consumer()
	if err := consumer.Subscribe("orders.created", func(_ context.Context, msg mq.Message) error {
		received <- msg
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	stop := runConsumer(t, consumer)
	defer stop()

	pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := broker.Producer().Publish(pubCtx, mq.Message{
		Topic:   "orders.created",
		Body:    []byte(`{"id":"o-1"}`),
		Headers: map[string]string{"x-trace": "abc"},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case msg := <-received:
		if msg.Topic != "orders.created" {
			t.Fatalf("topic = %q, want orders.created", msg.Topic)
		}
		if string(msg.Body) != `{"id":"o-1"}` {
			t.Fatalf("body = %q", msg.Body)
		}
		if msg.Headers["x-trace"] != "abc" {
			t.Fatalf("x-trace header lost: %+v", msg.Headers)
		}
		if msg.Headers[mq.HeaderMessageID] == "" {
			t.Fatalf("message id header missing: %+v", msg.Headers)
		}
		if msg.Attempt != 1 {
			t.Fatalf("attempt = %d, want 1", msg.Attempt)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestNATSBrokerDeadLetter(t *testing.T) {
	broker, url := testNATSBroker(t, platformasync.NATSBrokerOptions{MaxDeliver: 1, MaxAckPending: 4})

	var calls atomic.Int32
	consumer := broker.Consumer()
	if err := consumer.Subscribe("orders.failed", func(_ context.Context, _ mq.Message) error {
		calls.Add(1)
		return errors.New("boom")
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	stop := runConsumer(t, consumer)
	defer stop()

	pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := broker.Producer().Publish(pubCtx, mq.Message{Topic: "orders.failed", Body: []byte("payload")}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	var raw *nats.RawStreamMsg
	for time.Now().Before(deadline) {
		raw, err = js.GetLastMsg("async", "async.dlq.orders.failed")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dead letter not found: %v", err)
	}
	if cause := raw.Header.Get("mq.dlq_cause"); !strings.Contains(cause, "boom") {
		t.Fatalf("dlq cause = %q, want boom", cause)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1 (max_deliver=1)", got)
	}
}

func TestValidateAsyncNATSBroker(t *testing.T) {
	valid := func() *platformconfig.Config {
		cfg := iamBaseConfig()
		cfg.DB = platformconfig.DBConfig{Driver: "mysql", DSN: "iam:iam@tcp(127.0.0.1:3306)/iam"}
		cfg.Async.Enabled = true
		cfg.Async.Driver = platformconfig.AsyncDriverNATS
		cfg.Async.Broker = platformconfig.AsyncBrokerConfig{
			URL:           "nats://127.0.0.1:4222",
			Stream:        "async",
			SubjectPrefix: "async",
			AckWait:       30 * time.Second,
			MaxDeliver:    10,
		}
		return cfg
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid nats config: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*platformconfig.Config)
		want   string
	}{
		{"missing url", func(c *platformconfig.Config) { c.Async.Broker.URL = "" }, "async.broker.url is required"},
		{"bad scheme", func(c *platformconfig.Config) { c.Async.Broker.URL = "http://127.0.0.1:4222" }, "async.broker.url must start with"},
		{"bad stream", func(c *platformconfig.Config) { c.Async.Broker.Stream = "async.stream" }, "async.broker.stream"},
		{"bad subject prefix", func(c *platformconfig.Config) { c.Async.Broker.SubjectPrefix = "async.*" }, "async.broker.subject_prefix"},
		{"ack wait too small", func(c *platformconfig.Config) { c.Async.Broker.AckWait = 500 * time.Millisecond }, "async.broker.ack_wait"},
		{"max deliver too large", func(c *platformconfig.Config) { c.Async.Broker.MaxDeliver = 101 }, "async.broker.max_deliver"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid()
			tc.mutate(cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
		})
	}
}

func TestValidateAsyncNATSRequiresTLSInProd(t *testing.T) {
	cfg := prodBaseConfig()
	cfg.Async.Driver = platformconfig.AsyncDriverNATS
	cfg.Async.Broker = platformconfig.AsyncBrokerConfig{
		URL:           "nats://127.0.0.1:4222",
		Stream:        "async",
		SubjectPrefix: "async",
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "tls:// or wss://") {
		t.Fatalf("err=%v want TLS requirement", err)
	}

	cfg.Async.Broker.URL = "tls://nats.example.com:4222"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("prod tls nats config: %v", err)
	}
}

// TestValidateAsyncNATSDisabledSkipsBroker 校验 enabled=false 时仅声明驱动不触发 broker 必填。
func TestValidateAsyncNATSDisabledSkipsBroker(t *testing.T) {
	cfg := iamBaseConfig()
	cfg.Async.Driver = platformconfig.AsyncDriverNATS
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled nats driver: %v", err)
	}
}

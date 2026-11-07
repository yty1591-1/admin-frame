package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// iamModeEnforceValue 是 iam.mode=enforce 的取值；平台段不依赖 IAM 域包，
// 组合加载层把 iam.mode 显式传入，跨段规则仍能生效。
const iamModeEnforceValue = "enforce"

// validateAsync 校验异步运行时配置；未启用时只校验显式越界值。
func (c *Config) validateAsync(iamMode string) []error {
	var errs []error
	switch c.Async.Driver {
	case "", AsyncDriverDB:
	case AsyncDriverNATS:
		if c.Async.Enabled {
			errs = append(errs, c.validateAsyncBroker()...)
		}
	default:
		errs = append(errs, fmt.Errorf("async.driver %q is not implemented (only db and nats are supported)", c.Async.Driver))
	}
	if c.Async.Outbox.PollInterval != 0 &&
		(c.Async.Outbox.PollInterval < 100*time.Millisecond || c.Async.Outbox.PollInterval > time.Minute) {
		errs = append(errs, errors.New("async.outbox.poll_interval must be in [100ms, 1m]"))
	}
	if c.Async.Outbox.BatchSize != 0 && (c.Async.Outbox.BatchSize < 1 || c.Async.Outbox.BatchSize > 500) {
		errs = append(errs, errors.New("async.outbox.batch_size must be in [1, 500]"))
	}
	if c.Async.Outbox.Lease != 0 &&
		(c.Async.Outbox.Lease < 5*time.Second || c.Async.Outbox.Lease > 10*time.Minute) {
		errs = append(errs, errors.New("async.outbox.lease must be in [5s, 10m]"))
	}
	if c.Async.Outbox.MaxAttempts != 0 &&
		(c.Async.Outbox.MaxAttempts < 1 || c.Async.Outbox.MaxAttempts > 20) {
		errs = append(errs, errors.New("async.outbox.max_attempts must be in [1, 20]"))
	}
	if c.Async.Outbox.Concurrency != 0 &&
		(c.Async.Outbox.Concurrency < 1 || c.Async.Outbox.Concurrency > 16) {
		errs = append(errs, errors.New("async.outbox.concurrency must be in [1, 16]"))
	}
	if c.Async.Consumer.Concurrency != 0 &&
		(c.Async.Consumer.Concurrency < 1 || c.Async.Consumer.Concurrency > 32) {
		errs = append(errs, errors.New("async.consumer.concurrency must be in [1, 32]"))
	}
	if c.Async.Consumer.VisibilityTimeout != 0 &&
		(c.Async.Consumer.VisibilityTimeout < 5*time.Second ||
			c.Async.Consumer.VisibilityTimeout > 30*time.Minute) {
		errs = append(errs, errors.New("async.consumer.visibility_timeout must be in [5s, 30m]"))
	}
	switch c.Async.Consumer.RetryBackoff {
	case "", AsyncBackoffExponential, AsyncBackoffFixed:
	default:
		errs = append(errs, errors.New("async.consumer.retry_backoff must be exponential or fixed"))
	}
	if c.Async.Enabled && c.DB.Driver == "memory" {
		errs = append(errs, errors.New("async.enabled requires a persistent db.driver"))
	}
	if isProdLikeEnv(c.Env) && iamMode == iamModeEnforceValue && c.Async.Enabled && c.Async.Outbox.PublishInAPI {
		errs = append(errs, errors.New("async.outbox.publish_in_api must be false in prod/staging with iam.mode=enforce"))
	}
	return errs
}

// validateAsyncBroker 校验 NATS JetStream 连接与投递参数（async.driver=nats）。
func (c *Config) validateAsyncBroker() []error {
	var errs []error
	rawURL := strings.TrimSpace(c.Async.Broker.URL)
	if rawURL == "" {
		errs = append(errs, errors.New("async.broker.url is required when async.driver=nats"))
	} else {
		switch {
		case strings.HasPrefix(rawURL, "nats://"), strings.HasPrefix(rawURL, "tls://"),
			strings.HasPrefix(rawURL, "ws://"), strings.HasPrefix(rawURL, "wss://"):
		default:
			errs = append(errs, errors.New("async.broker.url must start with nats://, tls://, ws:// or wss://"))
		}
		if isProdLikeEnv(c.Env) &&
			!strings.HasPrefix(rawURL, "tls://") && !strings.HasPrefix(rawURL, "wss://") {
			errs = append(errs, errors.New("async.broker.url must use tls:// or wss:// in prod/staging"))
		}
	}
	if !validNATSStreamName(c.Async.Broker.Stream) {
		errs = append(errs, errors.New("async.broker.stream must be a non-empty ASCII alphanumeric/dash/underscore name"))
	}
	if !validNATSSubjectRoot(c.Async.Broker.SubjectPrefix) {
		errs = append(errs, errors.New("async.broker.subject_prefix must be a valid NATS subject prefix"))
	}
	if c.Async.Broker.AckWait != 0 &&
		(c.Async.Broker.AckWait < time.Second || c.Async.Broker.AckWait > 30*time.Minute) {
		errs = append(errs, errors.New("async.broker.ack_wait must be in [1s, 30m]"))
	}
	if c.Async.Broker.MaxDeliver != 0 &&
		(c.Async.Broker.MaxDeliver < 1 || c.Async.Broker.MaxDeliver > 100) {
		errs = append(errs, errors.New("async.broker.max_deliver must be in [1, 100]"))
	}
	return errs
}

// validNATSStreamName 校验 JetStream 流名称（NATS 不允许点号与通配符）。
// ValidNATSStreamName 报告是否为合法的 JetStream stream 名。
func ValidNATSStreamName(name string) bool { return validNATSStreamName(name) }

func validNATSStreamName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// validNATSSubjectRoot 校验主题前缀：允许点号分层，但禁止通配符、空白与空层。
// ValidNATSSubjectRoot 报告是否为合法的 subject 前缀。
func ValidNATSSubjectRoot(prefix string) bool { return validNATSSubjectRoot(prefix) }

func validNATSSubjectRoot(prefix string) bool {
	if prefix == "" || strings.ContainsAny(prefix, " \t\r\n*>") {
		return false
	}
	return !strings.HasPrefix(prefix, ".") && !strings.HasSuffix(prefix, ".") &&
		!strings.Contains(prefix, "..")
}

package async

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/admin-frame/admin-frame/pkg/mq"
)

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

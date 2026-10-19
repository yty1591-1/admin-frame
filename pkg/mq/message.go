package mq

import (
	"fmt"
	"time"
)

// Message 是与驱动无关的消息信封。
//
// Body 为原始字节；序列化由调用方负责。Key 用于分区/有序（可空）。
type Message struct {
	// ID 消息标识；Publish 前为空时由实现或 EnsureID 填充。
	ID string
	// Topic 主题 / 队列名。
	Topic string
	// Key 分区键或有序键（可空）。
	Key string
	// Headers 元数据（schema、租户、trace 等）。
	Headers map[string]string
	// Body 载荷。
	Body []byte
	// Timestamp 业务发生时间；零值时 Publish 可用当前时间。
	Timestamp time.Time
	// Attempt 当前投递次数（1 起）；由消费 Runner 维护。
	Attempt int
}

// Validate 校验发布前必填项。
func (m Message) Validate() error {
	if m.Topic == "" {
		return fmt.Errorf("%w: topic is required", ErrInvalidMessage)
	}
	return nil
}

// Clone 深拷贝 Headers 与 Body，避免并发篡改。
func (m Message) Clone() Message {
	out := m
	if m.Headers != nil {
		out.Headers = make(map[string]string, len(m.Headers))
		for k, v := range m.Headers {
			out.Headers[k] = v
		}
	}
	if m.Body != nil {
		out.Body = make([]byte, len(m.Body))
		copy(out.Body, m.Body)
	}
	return out
}

// WithHeader 返回带额外 header 的拷贝。
func (m Message) WithHeader(key, value string) Message {
	out := m.Clone()
	out.Headers = HeaderSet(out.Headers, key, value)
	return out
}

// SchemaVersion 读取 schema 版本 header。
func (m Message) SchemaVersion() string {
	return HeaderGet(m.Headers, HeaderSchemaVersion)
}

// MerchantID 读取商户 header。
func (m Message) MerchantID() string {
	return HeaderGet(m.Headers, HeaderMerchantID)
}

// UserID 读取用户 header。
func (m Message) UserID() string {
	return HeaderGet(m.Headers, HeaderUserID)
}

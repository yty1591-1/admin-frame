package mq

import stderrors "errors"

var (
	// ErrInvalidMessage 表示消息缺少必填字段。
	ErrInvalidMessage = stderrors.New("mq: invalid message")
	// ErrInvalidConfig 表示装配参数不合法。
	ErrInvalidConfig = stderrors.New("mq: invalid config")
	// ErrClosed 表示 Producer/Consumer/Broker 已关闭。
	ErrClosed = stderrors.New("mq: closed")
	// ErrNoHandler 表示主题无订阅者。
	ErrNoHandler = stderrors.New("mq: no handler")
	// ErrHandlerPanic 表示 Handler panic（已恢复并视为永久失败）。
	ErrHandlerPanic = stderrors.New("mq: handler panic")
)

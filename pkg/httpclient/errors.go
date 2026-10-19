package httpclient

import stderrors "errors"

var (
	// ErrInvalidConfig 表示客户端配置不合法。
	ErrInvalidConfig = stderrors.New("httpclient: invalid config")
	// ErrNilRequest 表示请求为 nil。
	ErrNilRequest = stderrors.New("httpclient: nil request")
)

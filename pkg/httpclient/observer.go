package httpclient

// AttemptObserver 观测每次 HTTP 尝试的结果（可选）。
//
// 由重试 Transport 在每次尝试结束时回调；未启用重试时每次请求只回调一次。
// 实现不得阻塞或做 IO，避免影响请求路径。
type AttemptObserver interface {
	// AttemptObserved 在尝试结束时调用：attempt 从 1 开始；
	// statusCode 在传输错误时为 0；err 为本次尝试的传输错误。
	AttemptObserved(attempt int, statusCode int, err error)
}

package idempotency

import (
	"bytes"
	"net/http"
)

// responseCapture 缓冲 handler 写出的状态码、头与体，便于 Complete。
type responseCapture struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
	wrote  bool
}

func newResponseCapture(w http.ResponseWriter) *responseCapture {
	return &responseCapture{ResponseWriter: w, status: http.StatusOK}
}

func (c *responseCapture) WriteHeader(code int) {
	if c.wrote {
		return
	}
	c.status = code
	c.wrote = true
}

func (c *responseCapture) Write(p []byte) (int, error) {
	if !c.wrote {
		c.WriteHeader(http.StatusOK)
	}
	return c.buf.Write(p)
}

func (c *responseCapture) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (c *responseCapture) flush() {
	// Header 已由 handler 写在底层 ResponseWriter 上，勿再次 Add 以免重复。
	c.ResponseWriter.WriteHeader(c.status)
	if c.buf.Len() > 0 {
		_, _ = c.ResponseWriter.Write(c.buf.Bytes())
	}
}

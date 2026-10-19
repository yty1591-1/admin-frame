package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"maps"
)

const (
	defaultContentType = "application/octet-stream"
	defaultListLimit   = 1000
	// DefaultMaxObjectBytes 默认单对象上限 32 MiB。
	DefaultMaxObjectBytes = 32 << 20
)

func cloneMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func contentTypeOrDefault(ct string) string {
	if ct == "" {
		return defaultContentType
	}
	return ct
}

func resolveMaxBytes(opts PutOptions, storeDefault int64) int64 {
	if opts.MaxBytes < 0 {
		return -1
	}
	if opts.MaxBytes > 0 {
		return opts.MaxBytes
	}
	if storeDefault > 0 {
		return storeDefault
	}
	return DefaultMaxObjectBytes
}

func listLimit(limit int) int {
	if limit <= 0 {
		return defaultListLimit
	}
	return limit
}

// limitedReader 读取超过 max 时返回 ErrTooLarge；由 newLimitedReader 构造。
type limitedReader struct {
	r      io.Reader
	remain int64
}

func newLimitedReader(r io.Reader, maxBytes int64) io.Reader {
	if maxBytes < 0 {
		return r
	}
	return &limitedReader{r: r, remain: maxBytes}
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.remain < 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > l.remain+1 {
		p = p[:l.remain+1]
	}
	n, err := l.r.Read(p)
	l.remain -= int64(n)
	if l.remain < 0 {
		return n, ErrTooLarge
	}
	return n, err
}

type hashingReader struct {
	r    io.Reader
	h    hash.Hash
	size int64
}

func newSHA256Reader(r io.Reader) *hashingReader {
	return &hashingReader{r: r, h: sha256.New()}
}

func (h *hashingReader) Read(p []byte) (int, error) {
	n, err := h.r.Read(p)
	if n > 0 {
		_, _ = h.h.Write(p[:n])
		h.size += int64(n)
	}
	return n, err
}

func (h *hashingReader) etag() string {
	return hex.EncodeToString(h.h.Sum(nil))
}

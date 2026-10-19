package idempotency

import "net/http"

// hopByHop 不落库、不回放的头部。
var hopByHop = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailers":            {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
	"Content-Length":      {},
}

func filterHeaders(h http.Header) http.Header {
	if h == nil {
		return make(http.Header)
	}
	out := make(http.Header, len(h))
	for k, vals := range h {
		if _, skip := hopByHop[http.CanonicalHeaderKey(k)]; skip {
			continue
		}
		for _, v := range vals {
			out.Add(k, v)
		}
	}
	return out
}

func writeStoredResponse(w http.ResponseWriter, rec Record) {
	for k, vals := range rec.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	status := rec.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if len(rec.Body) > 0 {
		_, _ = w.Write(rec.Body)
	}
}

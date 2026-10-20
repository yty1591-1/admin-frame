package cos

import (
	"net/http"
	"strings"

	tencentcos "github.com/tencentyun/cos-go-sdk-v5"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if tencentcos.IsNotFoundError(err) {
		return storage.ErrNotFound
	}
	return err
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func trimETag(s string) string {
	return strings.Trim(s, `"`)
}

func metaHeader(md map[string]string) *http.Header {
	if len(md) == 0 {
		return nil
	}
	h := &http.Header{}
	for k, v := range md {
		h.Add("x-cos-meta-"+k, v)
	}
	return h
}

func parseMeta(h http.Header) map[string]string {
	out := make(map[string]string)
	for k, vals := range h {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-cos-meta-") && len(vals) > 0 {
			out[strings.TrimPrefix(lk, "x-cos-meta-")] = vals[0]
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

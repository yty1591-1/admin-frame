package oss

import (
	"errors"
	"strings"

	alioss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var se *alioss.ServiceError
	if errors.As(err, &se) {
		if se.StatusCode == 404 {
			return storage.ErrNotFound
		}
		switch strings.ToLower(se.Code) {
		case "nosuchkey", "notfound", "nosuchbucket":
			return storage.ErrNotFound
		}
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

package obs

import (
	"errors"
	"strings"

	hwobs "github.com/huaweicloud/huaweicloud-sdk-go-obs/obs"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var oe hwobs.ObsError
	if errors.As(err, &oe) {
		if oe.StatusCode == 404 {
			return storage.ErrNotFound
		}
		switch strings.ToLower(oe.Code) {
		case "nosuchkey", "notfound", "nosuchbucket":
			return storage.ErrNotFound
		}
		if strings.HasPrefix(oe.Status, "404") {
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

func ensureHTTPEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return endpoint
	}
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	return "https://" + endpoint
}

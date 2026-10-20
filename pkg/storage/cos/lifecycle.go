package cos

import (
	"context"
	"strings"

	tencentcos "github.com/tencentyun/cos-go-sdk-v5"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

// PutLifecycle 实现 storage.LifecycleManager。
func (s *Store) PutLifecycle(ctx context.Context, rules []storage.LifecycleRule) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	if err := storage.ValidateLifecycleRules(rules); err != nil {
		return err
	}
	if len(rules) == 0 {
		_, err := s.client.Bucket.DeleteLifecycle(ctx)
		return mapErr(err)
	}
	cfgRules := make([]tencentcos.BucketLifecycleRule, 0, len(rules))
	for _, r := range rules {
		rule := tencentcos.BucketLifecycleRule{
			ID:     strings.TrimSpace(r.ID),
			Status: "Enabled",
			Filter: &tencentcos.BucketLifecycleFilter{
				Prefix: strings.Trim(r.Prefix, "/"),
			},
		}
		if r.ExpireDays > 0 {
			rule.Expiration = &tencentcos.BucketLifecycleExpiration{Days: r.ExpireDays}
		}
		if r.AbortIncompleteMultipartDays > 0 {
			rule.AbortIncompleteMultipartUpload = &tencentcos.BucketLifecycleAbortIncompleteMultipartUpload{
				DaysAfterInitiation: r.AbortIncompleteMultipartDays,
			}
		}
		cfgRules = append(cfgRules, rule)
	}
	_, err := s.client.Bucket.PutLifecycle(ctx, &tencentcos.BucketPutLifecycleOptions{Rules: cfgRules})
	return mapErr(err)
}

// GetLifecycle 实现 storage.LifecycleManager。
func (s *Store) GetLifecycle(ctx context.Context) ([]storage.LifecycleRule, error) {
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	out, _, err := s.client.Bucket.GetLifecycle(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	rules := make([]storage.LifecycleRule, 0, len(out.Rules))
	for _, r := range out.Rules {
		lr := storage.LifecycleRule{ID: r.ID}
		if r.Filter != nil {
			lr.Prefix = r.Filter.Prefix
		}
		if r.Expiration != nil {
			lr.ExpireDays = r.Expiration.Days
		}
		if r.AbortIncompleteMultipartUpload != nil {
			lr.AbortIncompleteMultipartDays = r.AbortIncompleteMultipartUpload.DaysAfterInitiation
		}
		rules = append(rules, lr)
	}
	return rules, nil
}

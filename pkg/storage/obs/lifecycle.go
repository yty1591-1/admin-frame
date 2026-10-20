package obs

import (
	"context"
	"strings"

	hwobs "github.com/huaweicloud/huaweicloud-sdk-go-obs/obs"

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
		_, err := s.client.DeleteBucketLifecycleConfiguration(s.bucket)
		return mapErr(err)
	}
	cfgRules := make([]hwobs.LifecycleRule, 0, len(rules))
	for _, r := range rules {
		rule := hwobs.LifecycleRule{
			ID:     strings.TrimSpace(r.ID),
			Prefix: strings.Trim(r.Prefix, "/"),
			Status: hwobs.RuleStatusEnabled,
		}
		if r.ExpireDays > 0 {
			rule.Expiration = hwobs.Expiration{Days: r.ExpireDays}
		}
		if r.AbortIncompleteMultipartDays > 0 {
			rule.AbortIncompleteMultipartUpload = hwobs.AbortIncompleteMultipartUpload{
				DaysAfterInitiation: r.AbortIncompleteMultipartDays,
			}
		}
		cfgRules = append(cfgRules, rule)
	}
	in := &hwobs.SetBucketLifecycleConfigurationInput{}
	in.Bucket = s.bucket
	in.LifecycleRules = cfgRules
	_, err := s.client.SetBucketLifecycleConfiguration(in)
	return mapErr(err)
}

// GetLifecycle 实现 storage.LifecycleManager。
func (s *Store) GetLifecycle(ctx context.Context) ([]storage.LifecycleRule, error) {
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	out, err := s.client.GetBucketLifecycleConfiguration(s.bucket)
	if err != nil {
		return nil, mapErr(err)
	}
	rules := make([]storage.LifecycleRule, 0, len(out.LifecycleRules))
	for _, r := range out.LifecycleRules {
		rules = append(rules, storage.LifecycleRule{
			ID:                           r.ID,
			Prefix:                       r.Prefix,
			ExpireDays:                   r.Expiration.Days,
			AbortIncompleteMultipartDays: r.AbortIncompleteMultipartUpload.DaysAfterInitiation,
		})
	}
	return rules, nil
}

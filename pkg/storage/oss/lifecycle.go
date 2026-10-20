package oss

import (
	"context"
	"strings"

	alioss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"

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
		_, err := s.client.DeleteBucketLifecycle(ctx, &alioss.DeleteBucketLifecycleRequest{
			Bucket: alioss.Ptr(s.bucket),
		})
		return mapErr(err)
	}
	cfgRules := make([]alioss.LifecycleRule, 0, len(rules))
	for _, r := range rules {
		rule := alioss.LifecycleRule{
			ID:     alioss.Ptr(strings.TrimSpace(r.ID)),
			Status: alioss.Ptr("Enabled"),
			Prefix: alioss.Ptr(strings.Trim(r.Prefix, "/")),
		}
		if r.ExpireDays > 0 {
			rule.Expiration = &alioss.LifecycleRuleExpiration{
				Days: alioss.Ptr(int32(r.ExpireDays)), //nolint:gosec // ValidateLifecycleRules 已限制上界为 MaxInt32
			}
		}
		if r.AbortIncompleteMultipartDays > 0 {
			rule.AbortMultipartUpload = &alioss.LifecycleRuleAbortMultipartUpload{
				Days: alioss.Ptr(int32(r.AbortIncompleteMultipartDays)), //nolint:gosec // ValidateLifecycleRules 已限制上界为 MaxInt32
			}
		}
		cfgRules = append(cfgRules, rule)
	}
	_, err := s.client.PutBucketLifecycle(ctx, &alioss.PutBucketLifecycleRequest{
		Bucket: alioss.Ptr(s.bucket),
		LifecycleConfiguration: &alioss.LifecycleConfiguration{
			Rules: cfgRules,
		},
	})
	return mapErr(err)
}

// GetLifecycle 实现 storage.LifecycleManager。
func (s *Store) GetLifecycle(ctx context.Context) ([]storage.LifecycleRule, error) {
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	out, err := s.client.GetBucketLifecycle(ctx, &alioss.GetBucketLifecycleRequest{
		Bucket: alioss.Ptr(s.bucket),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	if out.LifecycleConfiguration == nil {
		return nil, nil
	}
	src := out.LifecycleConfiguration.Rules
	rules := make([]storage.LifecycleRule, 0, len(src))
	for _, r := range src {
		lr := storage.LifecycleRule{
			ID:     alioss.ToString(r.ID),
			Prefix: alioss.ToString(r.Prefix),
		}
		if r.Expiration != nil && r.Expiration.Days != nil {
			lr.ExpireDays = int(*r.Expiration.Days)
		}
		if r.AbortMultipartUpload != nil && r.AbortMultipartUpload.Days != nil {
			lr.AbortIncompleteMultipartDays = int(*r.AbortMultipartUpload.Days)
		}
		rules = append(rules, lr)
	}
	return rules, nil
}

package s3

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

// PutLifecycle 实现 storage.LifecycleManager；空 rules 删除桶生命周期配置。
func (s *Store) PutLifecycle(ctx context.Context, rules []storage.LifecycleRule) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	if err := storage.ValidateLifecycleRules(rules); err != nil {
		return err
	}
	if len(rules) == 0 {
		_, err := s.client.DeleteBucketLifecycle(ctx, &s3.DeleteBucketLifecycleInput{
			Bucket: aws.String(s.bucket),
		})
		return err
	}
	cfgRules := make([]types.LifecycleRule, 0, len(rules))
	for _, r := range rules {
		prefix := strings.Trim(r.Prefix, "/")
		rule := types.LifecycleRule{
			ID:     aws.String(strings.TrimSpace(r.ID)),
			Status: types.ExpirationStatusEnabled,
			Filter: &types.LifecycleRuleFilter{
				Prefix: aws.String(prefix),
			},
		}
		if r.ExpireDays > 0 {
			rule.Expiration = &types.LifecycleExpiration{
				Days: aws.Int32(int32(r.ExpireDays)), //nolint:gosec // ValidateLifecycleRules 已限制上界为 MaxInt32
			}
		}
		if r.AbortIncompleteMultipartDays > 0 {
			rule.AbortIncompleteMultipartUpload = &types.AbortIncompleteMultipartUpload{
				DaysAfterInitiation: aws.Int32(int32(r.AbortIncompleteMultipartDays)), //nolint:gosec // ValidateLifecycleRules 已限制上界为 MaxInt32
			}
		}
		cfgRules = append(cfgRules, rule)
	}
	_, err := s.client.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{
		Bucket: aws.String(s.bucket),
		LifecycleConfiguration: &types.BucketLifecycleConfiguration{
			Rules: cfgRules,
		},
	})
	return err
}

// GetLifecycle 实现 storage.LifecycleManager。
func (s *Store) GetLifecycle(ctx context.Context) ([]storage.LifecycleRule, error) {
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	out, err := s.client.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{
		Bucket: aws.String(s.bucket),
	})
	if err != nil {
		return nil, err
	}
	rules := make([]storage.LifecycleRule, 0, len(out.Rules))
	for _, r := range out.Rules {
		lr := storage.LifecycleRule{
			ID: aws.ToString(r.ID),
		}
		if r.Filter != nil && r.Filter.Prefix != nil {
			lr.Prefix = aws.ToString(r.Filter.Prefix)
		} else if r.Prefix != nil { //nolint:staticcheck // 兼容仅设置旧式顶层 Prefix 的生命周期规则
			lr.Prefix = aws.ToString(r.Prefix) //nolint:staticcheck // 同上
		}
		if r.Expiration != nil && r.Expiration.Days != nil {
			lr.ExpireDays = int(aws.ToInt32(r.Expiration.Days))
		}
		if r.AbortIncompleteMultipartUpload != nil && r.AbortIncompleteMultipartUpload.DaysAfterInitiation != nil {
			lr.AbortIncompleteMultipartDays = int(aws.ToInt32(r.AbortIncompleteMultipartUpload.DaysAfterInitiation))
		}
		rules = append(rules, lr)
	}
	return rules, nil
}

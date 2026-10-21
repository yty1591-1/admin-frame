package cache

import (
	"fmt"

	"github.com/admin-frame/admin-frame/pkg/redis"
)

// DefaultKeyPrefix 是快照 key 的固定域段（对应 iam.cache.key_prefix 默认值）。
const DefaultKeyPrefix = "iam"

// SubjectKey 拼接主体级缓存 key。
//
// 形态：{env}:{channel}:{merchant_id}:{prefix}:subject:{subject_type}:{subject_id}
//
// 约束：
//   - channel / subject_type / subject_id 必填，禁止跨端、跨主体复用；
//   - B 端 merchant_id 必填（空值不表示「全部商户」），A 端为空时该段自动省略；
//   - 所有段不得含空白或控制字符，防止拼出歧义 key。
func SubjectKey(env, channel, merchantID, subjectType, subjectID string) (string, error) {
	if channel == "" || subjectType == "" || subjectID == "" {
		return "", fmt.Errorf("%w: channel, subject_type and subject_id are required", ErrInvalidConfig)
	}
	segments := []struct {
		name  string
		value string
	}{
		{"env", env},
		{"channel", channel},
		{"merchant_id", merchantID},
		{"subject_type", subjectType},
		{"subject_id", subjectID},
	}
	for _, segment := range segments {
		if segment.value == "" {
			continue
		}
		if !redis.ValidKeySegment(segment.value) {
			return "", fmt.Errorf("%w: invalid %s segment", ErrInvalidConfig, segment.name)
		}
	}
	return redis.Key(env, channel, merchantID, DefaultKeyPrefix, "subject", subjectType, subjectID), nil
}

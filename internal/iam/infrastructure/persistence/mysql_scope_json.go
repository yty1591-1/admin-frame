package persistence

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
)

// scopeValues 是 iam_role_data_scope.scope_values 的 JSON 结构。
//
// 只有 merchant_set 使用 merchant_ids；其他类型写空对象 {}，
// 保持「列 NOT NULL + 结构固定」，避免用空串表示空值。
type scopeValues struct {
	MerchantIDs []string `json:"merchant_ids,omitempty"`
}

// marshalScopeValues 把数据范围序列化为列值。
func marshalScopeValues(scope domain.DataScope) (string, error) {
	if scope.Type != domain.DataScopeMerchantSet {
		return "{}", nil
	}
	payload, err := json.Marshal(scopeValues{MerchantIDs: slices.Clone(scope.MerchantIDs)})
	if err != nil {
		return "", fmt.Errorf("marshal scope values for resource %q: %w", scope.Resource, err)
	}
	return string(payload), nil
}

// unmarshalScopeValues 解析列值；空值与未知字段不影响解析。
func unmarshalScopeValues(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var payload scopeValues
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal scope values: %w", err)
	}
	return payload.MerchantIDs, nil
}

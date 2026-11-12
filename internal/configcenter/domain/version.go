package domain

import (
	"fmt"
	"strings"
	"time"
)

// ItemVersion 是配置键的只追加版本快照，内容不可变。
type ItemVersion struct {
	ID            int64
	ItemID        int64
	Version       int64
	ValueText     string
	ValueHash     string
	ChangeSummary string
	CreatedBy     string
	CreatedAt     time.Time
}

// Validate 校验版本条目的不变量。
func (v ItemVersion) Validate() error {
	if v.ID <= 0 {
		return fmt.Errorf("configcenter: item version id is required")
	}
	if v.ItemID <= 0 {
		return fmt.Errorf("configcenter: item_id is required")
	}
	if v.Version < 1 {
		return fmt.Errorf("configcenter: item version must be >= 1")
	}
	if !ValidSHA256(v.ValueHash) {
		return fmt.Errorf("configcenter: value_hash must be 64 lowercase hex characters")
	}
	if v.ValueHash != HashValue(v.ValueText) {
		return fmt.Errorf("configcenter: value_hash does not match value_text")
	}
	if len(v.ChangeSummary) > MaxChangeSummary {
		return fmt.Errorf("configcenter: change_summary exceeds %d bytes", MaxChangeSummary)
	}
	if strings.TrimSpace(v.CreatedBy) == "" {
		return fmt.Errorf("configcenter: created_by is required")
	}
	if v.CreatedAt.IsZero() {
		return fmt.Errorf("configcenter: created_at is required")
	}
	return nil
}

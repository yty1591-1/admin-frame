// Package configclient 提供配置中心快照契约与进程内读取客户端。
//
// 设计边界（CFG-12 / ADR-0016）：
//   - 业务进程经 config-api HTTP 拉取；本地缓存仅作灾备；
//   - 信任靠 TLS + 服务身份，不再使用快照 Ed25519 签名；
//   - 快照不含任何密钥明文；敏感项只保留 value_hash；
//   - 本包不依赖 internal/，可被业务进程直接引用。
package configclient

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PayloadVersion 是快照规范化载荷的版本；变更载荷字段必须递增版本。
const PayloadVersion = "v1"

// 快照校验错误；调用方只允许按类别告警，不得把快照内容写入日志。
var (
	// ErrSnapshotHashMismatch 表示快照条目与 snapshot_hash 不一致。
	ErrSnapshotHashMismatch = errors.New("configclient: snapshot hash mismatch")
	// ErrSnapshotSizeMismatch 表示快照条目数与 snapshot_size 不一致。
	ErrSnapshotSizeMismatch = errors.New("configclient: snapshot size mismatch")
	// ErrSnapshotValueMismatch 表示快照取值与条目 value_hash 不一致。
	ErrSnapshotValueMismatch = errors.New("configclient: snapshot value does not match its hash")
)

// Item 是快照中的一条键版本指纹；敏感项只包含 value_hash。
type Item struct {
	ConfigKey   string `json:"config_key"`
	ItemID      int64  `json:"item_id"`
	ItemVersion int64  `json:"item_version"`
	ValueHash   string `json:"value_hash"`
}

// Snapshot 是一份生效配置快照。
//
// Values 只包含非敏感键的明文；Items 覆盖全部键（含敏感键）的指纹。
// KeyID / Signature 字段保留以兼容旧 JSON 缓存，运行期不再校验。
type Snapshot struct {
	Env            string            `json:"env"`
	Namespace      string            `json:"namespace"`
	ReleaseID      int64             `json:"release_id"`
	ReleaseNo      int64             `json:"release_no"`
	PointerVersion int64             `json:"pointer_version"`
	SnapshotHash   string            `json:"snapshot_hash"`
	SnapshotSize   int               `json:"snapshot_size"`
	KeyID          string            `json:"key_id,omitempty"`
	Signature      string            `json:"signature,omitempty"`
	GeneratedAt    time.Time         `json:"generated_at"`
	Items          []Item            `json:"items"`
	Values         map[string]string `json:"values"`
}

// HashItems 计算快照条目摘要。
//
// 规范化：按 config_key、item_id 排序后对 "key\0version\0value_hash\n"
// 取 SHA-256；与服务端 app.SnapshotHash 为同一算法，双方共用本实现。
func HashItems(items []Item) string {
	sorted := append([]Item(nil), items...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ConfigKey == sorted[j].ConfigKey {
			return sorted[i].ItemID < sorted[j].ItemID
		}
		return sorted[i].ConfigKey < sorted[j].ConfigKey
	})
	var builder strings.Builder
	for _, item := range sorted {
		builder.WriteString(item.ConfigKey)
		builder.WriteByte(0)
		builder.WriteString(strconv.FormatInt(item.ItemVersion, 10))
		builder.WriteByte(0)
		builder.WriteString(item.ValueHash)
		builder.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

// CanonicalPayload 生成规范化字节串（兼容旧缓存；ADR-0016 后不再用于签名）。
func (s Snapshot) CanonicalPayload() []byte {
	var builder strings.Builder
	builder.WriteString("config-snapshot:")
	builder.WriteString(PayloadVersion)
	builder.WriteByte(0)
	builder.WriteString(s.Env)
	builder.WriteByte(0)
	builder.WriteString(s.Namespace)
	builder.WriteByte(0)
	builder.WriteString(strconv.FormatInt(s.ReleaseID, 10))
	builder.WriteByte(0)
	builder.WriteString(strconv.FormatInt(s.ReleaseNo, 10))
	builder.WriteByte(0)
	builder.WriteString(s.SnapshotHash)
	builder.WriteByte(0)
	builder.WriteString(strconv.Itoa(s.SnapshotSize))
	builder.WriteByte('\n')
	return []byte(builder.String())
}

// ValidateIntegrity 校验快照哈希与取值一致性（不含签名）。
func (s Snapshot) ValidateIntegrity() error {
	if len(s.Items) != s.SnapshotSize {
		return ErrSnapshotSizeMismatch
	}
	if HashItems(s.Items) != s.SnapshotHash {
		return ErrSnapshotHashMismatch
	}
	hashes := make(map[string]string, len(s.Items))
	for _, item := range s.Items {
		hashes[item.ConfigKey] = item.ValueHash
	}
	for key, value := range s.Values {
		expected, ok := hashes[key]
		if !ok || expected != hashValue(value) {
			return ErrSnapshotValueMismatch
		}
	}
	return nil
}

// hashValue 计算取值指纹；与 internal/configcenter/domain.HashValue 一致。
func hashValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// Package configclient 提供配置中心快照的签名契约与进程内读取客户端。
//
// 设计边界（CFG-12 / CC-F-37 / CC-F-45）：
//   - 快照只作冷启动与灾备兜底，有中心时以中心为准；
//   - 快照签名使用 Ed25519，验签失败拒绝装载，不允许降级为「信任未签名数据」；
//   - 快照不含任何密钥明文；敏感项只保留 value_hash；
//   - 本包不依赖 internal/，可被业务进程直接引用。
package configclient

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/pkg/crypto"
)

// PayloadVersion 是签名快照规范化载荷的版本；变更载荷字段必须递增版本。
const PayloadVersion = "v1"

// 快照校验错误；调用方只允许按类别告警，不得把快照内容写入日志。
var (
	// ErrSnapshotUnsigned 表示快照缺少签名或 key_id。
	ErrSnapshotUnsigned = errors.New("configclient: snapshot is not signed")
	// ErrSnapshotUnknownKeyID 表示签名 key_id 不在本地公钥集合内。
	ErrSnapshotUnknownKeyID = errors.New("configclient: snapshot signed by unknown key id")
	// ErrSnapshotHashMismatch 表示快照条目与 snapshot_hash 不一致。
	ErrSnapshotHashMismatch = errors.New("configclient: snapshot hash mismatch")
	// ErrSnapshotSizeMismatch 表示快照条目数与 snapshot_size 不一致。
	ErrSnapshotSizeMismatch = errors.New("configclient: snapshot size mismatch")
	// ErrSnapshotValueMismatch 表示快照取值与条目 value_hash 不一致。
	ErrSnapshotValueMismatch = errors.New("configclient: snapshot value does not match its hash")
	// ErrSnapshotSignatureInvalid 表示 Ed25519 验签失败。
	ErrSnapshotSignatureInvalid = errors.New("configclient: snapshot signature is invalid")
)

// Item 是快照中的一条键版本指纹；敏感项只包含 value_hash。
type Item struct {
	ConfigKey   string `json:"config_key"`
	ItemID      int64  `json:"item_id"`
	ItemVersion int64  `json:"item_version"`
	ValueHash   string `json:"value_hash"`
}

// Snapshot 是一份带签名的生效快照。
//
// Values 只包含非敏感键的明文；Items 覆盖全部键（含敏感键）的指纹，
// 客户端据此重建 snapshot_hash 并校验签名，避免只信任服务端返回的摘要。
type Snapshot struct {
	Env            string            `json:"env"`
	Namespace      string            `json:"namespace"`
	ReleaseID      int64             `json:"release_id"`
	ReleaseNo      int64             `json:"release_no"`
	PointerVersion int64             `json:"pointer_version"`
	SnapshotHash   string            `json:"snapshot_hash"`
	SnapshotSize   int               `json:"snapshot_size"`
	KeyID          string            `json:"key_id"`
	Signature      string            `json:"signature"`
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

// CanonicalPayload 生成被签名的规范化字节串。
//
// 字段固定、以 NUL 分隔、以换行结尾；不包含时间戳与取值，时间戳不参与
// 安全判定，取值由 Items 的 value_hash 间接覆盖。
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

// Signer 使用 Ed25519 私钥对快照元数据签名（管理面发布时使用）。
type Signer struct {
	keyID      string
	privateKey ed25519.PrivateKey
}

// NewSigner 构造签名器；key_id 与私钥必填，私钥长度非法直接拒绝。
func NewSigner(keyID string, privateKey ed25519.PrivateKey) (*Signer, error) {
	if strings.TrimSpace(keyID) == "" {
		return nil, fmt.Errorf("configclient: signing key id is required")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("configclient: invalid ed25519 private key size")
	}
	key := make(ed25519.PrivateKey, len(privateKey))
	copy(key, privateKey)
	return &Signer{keyID: keyID, privateKey: key}, nil
}

// KeyID 返回签名使用的 key id。
func (s *Signer) KeyID() string {
	if s == nil {
		return ""
	}
	return s.keyID
}

// Sign 返回 key id 与 base64 签名；不修改传入快照。
func (s *Signer) Sign(snapshot Snapshot) (keyID, signature string, err error) {
	if s == nil {
		return "", "", fmt.Errorf("configclient: signer is nil")
	}
	raw, err := crypto.SignEd25519(s.privateKey, snapshot.CanonicalPayload())
	if err != nil {
		return "", "", err
	}
	return s.keyID, base64.StdEncoding.EncodeToString(raw), nil
}

// Verifier 按 key_id 选择公钥验签，支持多版本密钥轮换。
type Verifier struct {
	keys map[string]ed25519.PublicKey
}

// NewVerifier 构造验签器；公钥集合为空直接拒绝（避免「无公钥即放行」）。
func NewVerifier(keys map[string]ed25519.PublicKey) (*Verifier, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("configclient: verifier requires at least one public key")
	}
	copied := make(map[string]ed25519.PublicKey, len(keys))
	for keyID, publicKey := range keys {
		if strings.TrimSpace(keyID) == "" {
			return nil, fmt.Errorf("configclient: public key id must not be empty")
		}
		if len(publicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("configclient: invalid ed25519 public key for key id %q", keyID)
		}
		copied[keyID] = append(ed25519.PublicKey(nil), publicKey...)
	}
	return &Verifier{keys: copied}, nil
}

// ParsePublicKeys 解析 "kid=base64,kid2=base64" 形式的公钥配置。
//
// 供进程从注入的密钥材料装配验签器；不接受空 key id 或非法 base64。
func ParsePublicKeys(raw string) (map[string]ed25519.PublicKey, error) {
	keys := make(map[string]ed25519.PublicKey)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		keyID, encoded, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("configclient: public key entry %q must be kid=base64", keyID)
		}
		keyID = strings.TrimSpace(keyID)
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil {
			return nil, fmt.Errorf("configclient: public key %q is not valid base64", keyID)
		}
		if len(decoded) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("configclient: public key %q has invalid size", keyID)
		}
		keys[keyID] = ed25519.PublicKey(decoded)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("configclient: no public key configured")
	}
	return keys, nil
}

// Verify 校验快照完整性：
//  1. 条目重建的 snapshot_hash 与声明一致；
//  2. 条目数与 snapshot_size 一致；
//  3. 每个取值与其 value_hash 一致；
//  4. Ed25519 签名有效且 key_id 已知。
func (v *Verifier) Verify(snapshot Snapshot) error {
	if strings.TrimSpace(snapshot.Signature) == "" || strings.TrimSpace(snapshot.KeyID) == "" {
		return ErrSnapshotUnsigned
	}
	publicKey, ok := v.keys[snapshot.KeyID]
	if !ok {
		return ErrSnapshotUnknownKeyID
	}
	if len(snapshot.Items) != snapshot.SnapshotSize {
		return ErrSnapshotSizeMismatch
	}
	if HashItems(snapshot.Items) != snapshot.SnapshotHash {
		return ErrSnapshotHashMismatch
	}
	hashes := make(map[string]string, len(snapshot.Items))
	for _, item := range snapshot.Items {
		hashes[item.ConfigKey] = item.ValueHash
	}
	for key, value := range snapshot.Values {
		expected, ok := hashes[key]
		if !ok || expected != hashValue(value) {
			return ErrSnapshotValueMismatch
		}
	}
	raw, err := base64.StdEncoding.DecodeString(snapshot.Signature)
	if err != nil {
		return ErrSnapshotSignatureInvalid
	}
	if err := crypto.VerifyEd25519(publicKey, snapshot.CanonicalPayload(), raw); err != nil {
		return ErrSnapshotSignatureInvalid
	}
	return nil
}

// hashValue 计算取值指纹；与 internal/configcenter/domain.HashValue 一致。
func hashValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

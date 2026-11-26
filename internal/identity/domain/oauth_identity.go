package domain

import (
	"strings"
	"time"
)

// OAuth 身份映射字段上限，与 migrations/000001 / 000027 的列宽一致。
const (
	// MaxOAuthProviderLen 提供方标识最大字节数。
	//
	// 000027 起列宽扩到 64，容纳 scim:<provider> 形式；SCIM provider 上限 32 字节。
	MaxOAuthProviderLen = 64
	// MaxOAuthProviderSubjectLen 提供方主体标识最大字节数。
	MaxOAuthProviderSubjectLen = 255
)

// OAuthIdentity 是外部身份与站内账号的映射。
//
// 唯一约束为 (provider, provider_subject)：同一提供方主体只能映射一个账号。
type OAuthIdentity struct {
	// ID 雪花 ID。
	ID string
	// AccountID 关联的站内账号。
	AccountID string
	// Provider 提供方标识，如 github / oidc-corp。
	Provider string
	// ProviderSubject 提供方侧主体标识（userinfo 的 sub）。
	ProviderSubject string
	// ProviderSubjectIndex 提供方主体的确定性索引；S5 起作为唯一键。
	ProviderSubjectIndex string
	// ProviderSubjectCiphertext 提供方主体密文；AAD 绑定映射主键。
	ProviderSubjectCiphertext []byte
	// CreatedAt / UpdatedAt UTC。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewOAuthIdentityParams 创建映射的入参。
type NewOAuthIdentityParams struct {
	ID              string
	AccountID       string
	Provider        string
	ProviderSubject string
	// Now 创建时间；零值时取当前 UTC 时间。
	Now time.Time
}

// NewOAuthIdentity 创建外部身份映射。
func NewOAuthIdentity(p NewOAuthIdentityParams) (*OAuthIdentity, error) {
	if strings.TrimSpace(p.ID) == "" {
		return nil, errInvalid(ErrEmptyInput, "oauth identity id is required")
	}
	if strings.TrimSpace(p.AccountID) == "" || len(p.AccountID) > MaxAccountIDLen {
		return nil, errInvalid(ErrEmptyInput, "oauth identity account id is required and must be at most %d bytes", MaxAccountIDLen)
	}
	provider := strings.TrimSpace(p.Provider)
	if provider == "" || len(provider) > MaxOAuthProviderLen {
		return nil, errInvalid(ErrInvalidOAuthIdentity, "oauth provider is required and must be at most %d bytes", MaxOAuthProviderLen)
	}
	subject := strings.TrimSpace(p.ProviderSubject)
	if subject == "" || len(subject) > MaxOAuthProviderSubjectLen {
		return nil, errInvalid(ErrInvalidOAuthIdentity, "oauth provider subject is required and must be at most %d bytes", MaxOAuthProviderSubjectLen)
	}
	now := utcNow(p.Now)
	return &OAuthIdentity{
		ID:              p.ID,
		AccountID:       p.AccountID,
		Provider:        provider,
		ProviderSubject: subject,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

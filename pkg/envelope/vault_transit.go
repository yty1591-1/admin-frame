package envelope

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// VaultTransit 通过 Vault Transit encrypt/decrypt API wrap / unwrap DEK。
//
// 注意：这与 internal/platform/secrets 的 KV Get 语义不同，禁止混用。
type VaultTransit struct {
	addr   string
	token  string
	keyName string
	client *http.Client
	keyID  string
}

// NewVaultTransit 构造 Transit 客户端；keyID 形如 transit/<keyName>。
func NewVaultTransit(addr, token, keyName string) (*VaultTransit, error) {
	addr = strings.TrimRight(strings.TrimSpace(addr), "/")
	keyName = strings.TrimSpace(keyName)
	if addr == "" || token == "" || keyName == "" {
		return nil, fmt.Errorf("envelope: vault transit addr/token/key required")
	}
	return &VaultTransit{
		addr:    addr,
		token:   token,
		keyName: keyName,
		client:  &http.Client{Timeout: 10 * time.Second},
		keyID:   "transit/" + keyName,
	}, nil
}

// KeyID 返回 Transit 密钥标识。
func (v *VaultTransit) KeyID() string { return v.keyID }

type vaultEncryptReq struct {
	Plaintext string `json:"plaintext"`
}

type vaultCipherResp struct {
	Data struct {
		Ciphertext string `json:"ciphertext"`
		Plaintext  string `json:"plaintext"`
	} `json:"data"`
}

// Wrap 调用 transit/encrypt。
func (v *VaultTransit) Wrap(dek []byte) ([]byte, string, error) {
	body, _ := json.Marshal(vaultEncryptReq{Plaintext: base64.StdEncoding.EncodeToString(dek)})
	raw, err := v.post("/v1/transit/encrypt/"+v.keyName, body)
	if err != nil {
		return nil, "", err
	}
	var resp vaultCipherResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, "", err
	}
	if resp.Data.Ciphertext == "" {
		return nil, "", fmt.Errorf("envelope: vault encrypt returned empty ciphertext")
	}
	return []byte(resp.Data.Ciphertext), v.keyID, nil
}

// Unwrap 调用 transit/decrypt。
func (v *VaultTransit) Unwrap(wrapped []byte, keyID string) ([]byte, error) {
	if keyID != "" && keyID != v.keyID {
		return nil, fmt.Errorf("envelope: vault key_id mismatch")
	}
	payload, _ := json.Marshal(map[string]string{"ciphertext": string(wrapped)})
	raw, err := v.post("/v1/transit/decrypt/"+v.keyName, payload)
	if err != nil {
		return nil, err
	}
	var resp vaultCipherResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	dek, err := base64.StdEncoding.DecodeString(resp.Data.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("envelope: decode vault plaintext: %w", err)
	}
	return dek, nil
}

func (v *VaultTransit) post(path string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, v.addr+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", v.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("envelope: vault %s status %d", path, resp.StatusCode)
	}
	return raw, nil
}

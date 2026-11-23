package configboot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ResetInvalidValues 请求 config-api 把给定键重置为声明默认值。
//
// 取值由 config-api 按 key spec（prod-like overlay 优先）解析，调用方只声明
// 键，避免把启动自愈通道变成任意写入通道；refs 形如 namespace/key。
func ResetInvalidValues(ctx context.Context, baseURL string, client *http.Client,
	identity, env string, refs []string) error {
	if len(refs) == 0 {
		return nil
	}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return fmt.Errorf("configboot: config-api url is required to reset defaults")
	}
	body, err := json.Marshal(map[string]any{"env": env, "refs": refs})
	if err != nil {
		return fmt.Errorf("configboot: encode reset defaults request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/internal/v1/configs/repair/reset-defaults", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("configboot: build reset defaults request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if identity = strings.TrimSpace(identity); identity != "" {
		req.Header.Set("X-Service-Identity", identity)
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("configboot: call reset defaults: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return fmt.Errorf("configboot: read reset defaults response: %w", readErr)
	}
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if unmarshalErr := json.Unmarshal(payload, &envelope); unmarshalErr != nil {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// 符合信封约定的响应才会走到这里；解析失败时只保留状态码信息。
			return nil
		}
		return fmt.Errorf("configboot: reset defaults failed: status=%d", resp.StatusCode)
	}
	if envelope.Code != 0 {
		return fmt.Errorf("configboot: reset defaults failed: code=%d message=%s",
			envelope.Code, envelope.Message)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("configboot: reset defaults failed: status=%d", resp.StatusCode)
	}
	return nil
}

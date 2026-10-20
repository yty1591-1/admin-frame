package webhook

import (
	"encoding/base64"
	"strconv"
	"strings"
)

const (
	maxIDBytes              = 200
	maxTimestampBytes       = 19 // math.MaxInt64 的十进制长度
	maxSignatureHeaderBytes = 8 << 10
	maxSignatureEntries     = 8
)

// signingMessage 构造签名原文：id + "." + 十进制时间戳 + "." + 原始 body。
//
// body 原样拼接，不做换行归一化或 JSON 重编码；否则签名必然与发送方不一致。
func signingMessage(id string, timestamp int64, body []byte) []byte {
	message := make([]byte, 0, len(id)+1+maxTimestampBytes+1+len(body))
	message = append(message, id...)
	message = append(message, '.')
	message = strconv.AppendInt(message, timestamp, 10)
	message = append(message, '.')
	return append(message, body...)
}

// formatSignature 将签名编码为 <version>,<base64>。
func formatSignature(version string, signature []byte) string {
	return version + "," + base64.StdEncoding.EncodeToString(signature)
}

// validateID 校验消息 ID：可见 ASCII、长度 1～200，禁止空白与控制字符。
//
// 调用方需先区分「缺失」与「非法」；空串不应传入。
func validateID(name, id string) error {
	if len(id) > maxIDBytes {
		return headerError(ErrMalformedHeader, name, "exceeds 200 bytes")
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x21 || id[i] > 0x7e {
			return headerError(ErrMalformedHeader, name, "contains non-printable characters")
		}
	}
	return nil
}

// parseTimestamp 严格解析 Unix 秒；拒绝符号、空白、前导零与超长值。
//
// 前导零会让「头原值」与「重新格式化值」不一致，必须在入口拒绝。
func parseTimestamp(name, raw string) (int64, error) {
	if raw == "" {
		return 0, headerError(ErrMissingHeader, name, "is empty")
	}
	if len(raw) > maxTimestampBytes {
		return 0, headerError(ErrMalformedHeader, name, "is out of range")
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, headerError(ErrMalformedHeader, name, "must be decimal Unix seconds")
		}
	}
	if len(raw) > 1 && raw[0] == '0' {
		return 0, headerError(ErrMalformedHeader, name, "must not have leading zeros")
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, headerError(ErrMalformedHeader, name, "is out of range")
	}
	return seconds, nil
}

// parseSignatures 解析签名头，返回与配置版本匹配的已解码签名。
//
// 头格式：<version>,<base64>；多个条目以空格分隔。未知版本的条目会被跳过，
// 以支持发送方灰度升级；若没有任何条目匹配配置版本，返回 ErrUnsupportedVersion。
// 容忍标准与无填充 Base64（部分厂商会去掉 '='）。
func parseSignatures(name, raw, version string) (signatures [][]byte, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, headerError(ErrMissingHeader, name, "is empty")
	}
	if len(raw) > maxSignatureHeaderBytes {
		return nil, headerError(ErrMalformedHeader, name, "exceeds 8 KiB")
	}
	entries := strings.Fields(raw)
	if len(entries) > maxSignatureEntries {
		return nil, headerError(ErrMalformedHeader, name, "contains too many signatures")
	}

	out := make([][]byte, 0, len(entries))
	matched := false
	for _, entry := range entries {
		entryVersion, encoded, ok := strings.Cut(entry, ",")
		if !ok || encoded == "" {
			return nil, headerError(ErrMalformedHeader, name, "must be <version>,<base64>")
		}
		if entryVersion != version {
			continue
		}
		matched = true
		signature, decodeErr := decodeSignature(encoded)
		if decodeErr != nil {
			return nil, headerError(ErrMalformedHeader, name, "contains invalid base64")
		}
		if len(signature) == 0 {
			return nil, headerError(ErrMalformedHeader, name, "contains empty signature")
		}
		out = append(out, signature)
	}
	if !matched {
		return nil, unsupportedVersion(version)
	}
	return out, nil
}

func decodeSignature(encoded string) ([]byte, error) {
	if signature, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		return signature, nil
	}
	return base64.RawStdEncoding.DecodeString(encoded)
}

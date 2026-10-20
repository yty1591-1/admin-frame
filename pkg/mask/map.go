package mask

import "strings"

// 已知敏感字段名（小写，去下划线/连字符后匹配亦可）。
var fieldMaskers = map[string]func(string) string{
	"phone":         Phone,
	"mobile":        Phone,
	"mobile_phone":  Phone,
	"tel":           Phone,
	"telephone":     Phone,
	"id_card":       IDCard,
	"idcard":        IDCard,
	"identity_card": IDCard,
	"id_no":         IDCard,
	"bank_card":     BankCard,
	"bankcard":      BankCard,
	"card_no":       BankCard,
	"card_number":   BankCard,
	"email":         Email,
	"mail":          Email,
	"name":          Name,
	"real_name":     Name,
	"user_name":     Name,
	"username":      Name, // 弱敏感：仍做轻度掩码
	"token":         Token,
	"access_token":  Token,
	"refresh_token": Token,
	"id_token":      Token,
	"authorization": Authorization,
	"password":      func(string) string { return "***" },
	"passwd":        func(string) string { return "***" },
	"pwd":           func(string) string { return "***" },
	"secret":        func(string) string { return "***" },
	"api_key":       func(string) string { return "***" },
	"private_key":   func(string) string { return "***" },
}

// Map 按已知敏感键名脱敏 map 值，返回新 map（不修改入参）。
// 未识别的键原样拷贝；值仍建议调用方避免放入高敏原文。
func Map(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = Field(k, v)
	}
	return out
}

// Field 按字段名选择脱敏策略；未知字段对值做 Text 启发式扫描。
func Field(key, value string) string {
	if value == "" {
		return ""
	}
	norm := normalizeFieldKey(key)
	if fn, ok := fieldMaskers[norm]; ok {
		return fn(value)
	}
	// 模糊：整词边界（prefix_/\_suffix），避免 filename 误伤
	for hint, fn := range fieldMaskers {
		if strings.HasSuffix(norm, "_"+hint) || strings.HasPrefix(norm, hint+"_") {
			return fn(value)
		}
	}
	return Text(value)
}

func normalizeFieldKey(key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, " ", "_")
	return key
}

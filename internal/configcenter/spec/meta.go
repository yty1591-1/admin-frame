package spec

import (
	"strings"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
)

// 展示元数据：把代码里的 key spec 投影成安装向导可以渲染的表单结构。
//
// 这里只做「派生」不做「声明」：分组、短标签、控件类型全部由 key 前缀、
// ValueType 与 Description 推导，因此新增键无需再维护一份 UI 清单；
// 需要覆盖时再回到 KeySpec 增补显式字段（ADR-0018 P2）。

// Group 是表单分组。
type Group struct {
	// ID 是稳定标识（点分前缀），前端用于折叠态与锚点。
	ID string
	// Title 是中文标题。
	Title string
	// Order 是表单顺序，越小越靠前。
	Order int
}

// groupTitles 定义已知分组的标题与顺序；未命中的前缀归入 other。
var groupTitles = map[string]struct {
	Title string
	Order int
}{
	"db":           {"数据库", 10},
	"redis":        {"缓存", 20},
	"auth":         {"认证", 30},
	"http":         {"HTTP 与超时", 40},
	"server":       {"服务端", 50},
	"storage":      {"对象存储", 60},
	"async":        {"异步与 Outbox", 70},
	"idempotency":  {"幂等", 80},
	"id":           {"ID 生成", 90},
	"rate_limit":   {"限流", 100},
	"audit":        {"审计", 110},
	"iam":          {"权限与审批", 120},
	"identity":     {"身份与登录", 130},
	"tenant":       {"租户", 140},
	"notification": {"通知通道", 150},
	"tracing":      {"链路追踪", 160},
	"log":          {"日志", 170},
	"worker":       {"后台任务", 180},
	"smtp":         {"SMTP 投递", 190},
	"platform":     {"平台元信息", 200},
}

// subgroupTitles 定义二级分组（家族键过多时拆分），键为两段前缀。
var subgroupTitles = map[string]struct {
	Title string
	Order int
}{
	"identity.sms":          {"短信", 131},
	"identity.mfa":          {"多因素认证", 132},
	"identity.oauth":        {"OAuth 登录", 133},
	"identity.saml":         {"SAML 登录", 134},
	"identity.password":     {"口令策略", 135},
	"notification.channels": {"通知通道", 151},
}

// otherGroup 是未命中前缀时的兜底分组。
var otherGroup = Group{ID: "other", Title: "其他", Order: 900}

// GroupOf 推导键所属分组：优先二级分组，其次一级前缀，最后 other。
func GroupOf(key string) Group {
	parts := strings.Split(key, ".")
	if len(parts) >= 3 {
		if meta, ok := subgroupTitles[parts[0]+"."+parts[1]]; ok {
			return Group{ID: parts[0] + "." + parts[1], Title: meta.Title, Order: meta.Order}
		}
	}
	prefix := parts[0]
	if meta, ok := groupTitles[prefix]; ok {
		return Group{ID: prefix, Title: meta.Title, Order: meta.Order}
	}
	return otherGroup
}

// NamespaceTitle 返回命名空间的展示标题与顺序。
func NamespaceTitle(namespace string) (title string, order int) {
	switch namespace {
	case "platform":
		return "平台与基础设施", 10
	case "iam":
		return "权限与审批", 20
	case "identity":
		return "身份与登录", 30
	case "audit":
		return "审计", 40
	case "tenant":
		return "租户", 50
	case "notification":
		return "通知通道", 60
	default:
		return namespace, 900
	}
}

// DeriveLabel 从描述取首个子句作为表单短标签（描述全文仍作为帮助文本）。
func DeriveLabel(keySpec domain.KeySpec) string {
	description := strings.TrimSpace(keySpec.Description)
	for _, sep := range []string{"（", "(", "，", ",", "；", ";", "。"} {
		if idx := strings.Index(description, sep); idx > 0 {
			description = strings.TrimSpace(description[:idx])
			break
		}
	}
	if description == "" {
		return keySpec.Key
	}
	return description
}

// WidgetFor 推导表单控件类型，前端据此选择组件。
func WidgetFor(keySpec domain.KeySpec) string {
	if keySpec.Sensitive {
		return "password"
	}
	if len(keySpec.Enum) > 0 {
		return "select"
	}
	switch keySpec.ValueType {
	case domain.ValueTypeBool:
		return "switch"
	case domain.ValueTypeInt:
		return "number"
	case domain.ValueTypeDuration:
		return "duration"
	case domain.ValueTypeJSON:
		return "code"
	default:
		return "text"
	}
}

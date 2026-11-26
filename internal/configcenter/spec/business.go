package spec

import (
	"strconv"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
)

// 业务域配置命名空间与 owner（CFG-07：每个键必须有明确负责人）。
const (
	// NamespaceIdentity 是身份域配置命名空间。
	NamespaceIdentity = "identity"
	// NamespaceAudit 是审计域配置命名空间。
	NamespaceAudit = "audit"
	// NamespaceTenant 是租户域配置命名空间。
	NamespaceTenant = "tenant"
	// NamespaceNotification 是通知域配置命名空间。
	NamespaceNotification = "notification"

	ownerIdentity     = "team-identity"
	ownerAudit        = "team-audit"
	ownerTenant       = "team-tenant"
	ownerNotification = "team-notification"
)

// namespaceSpecs 是某业务域的键声明工厂。
//
// 业务域配置目前均在启动期读取，统一 reloadable=false；热更白名单由平台段
// 单独声明。默认值与各域 ApplyDefaults / Normalize 的生效值保持一致，
// 由 appconfig 的默认值一致性测试守护。
type namespaceSpecs struct {
	namespace string
	owner     string
}

func (n namespaceSpecs) base(key, defaultValue, description string, valueType domain.ValueType) domain.KeySpec {
	return domain.KeySpec{
		Namespace:   n.namespace,
		Key:         key,
		ValueType:   valueType,
		Default:     defaultValue,
		Description: description,
		Owner:       n.owner,
		ReloadMode:  domain.ReloadStartup,
		Reloadable:  false,
	}
}

func (n namespaceSpecs) str(key, defaultValue, description string) domain.KeySpec {
	return n.base(key, defaultValue, description, domain.ValueTypeString)
}

// floatString 以字符串承载浮点速率（CC-R-11：无独立 float ValueType 时的登记方式）。
func (n namespaceSpecs) floatString(key, defaultValue, description string) domain.KeySpec {
	spec := n.str(key, defaultValue, description)
	spec.Pattern = `^[0-9]+(\.[0-9]+)?$`
	return spec
}

func (n namespaceSpecs) enum(key, defaultValue, description string, allowed ...string) domain.KeySpec {
	spec := n.str(key, defaultValue, description)
	spec.Enum = allowed
	return spec
}

func (n namespaceSpecs) integer(key string, defaultValue int64, description string) domain.KeySpec {
	return n.base(key, strconv.FormatInt(defaultValue, 10), description, domain.ValueTypeInt)
}

// integerMin 构造带下限的整数键（CC-R-10）。
func (n namespaceSpecs) integerMin(key string, defaultValue, minValue int64, description string) domain.KeySpec {
	spec := n.integer(key, defaultValue, description)
	minCopy := minValue
	spec.MinInt = &minCopy
	return spec
}

func (n namespaceSpecs) boolean(key string, defaultValue bool, description string) domain.KeySpec {
	return n.base(key, strconv.FormatBool(defaultValue), description, domain.ValueTypeBool)
}

func (n namespaceSpecs) duration(key, defaultValue, description string) domain.KeySpec {
	return n.base(key, defaultValue, description, domain.ValueTypeDuration)
}

// sensitiveAuto 登记敏感键（仅进 config_secret，不进普通 Item 播种；CC-R-05）。
func (n namespaceSpecs) sensitiveAuto(key, description string, rule *domain.SecretRule) domain.KeySpec {
	return domain.KeySpec{
		Namespace:   n.namespace,
		Key:         key,
		ValueType:   domain.ValueTypeString,
		Description: description,
		Owner:       n.owner,
		ReloadMode:  domain.ReloadStartup,
		Reloadable:  false,
		Sensitive:   true,
		SecretRule:  rule,
	}
}

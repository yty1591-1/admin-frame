// Package config 是通知域的渠道配置（ENT-10 NT-04）。
//
// 只描述「用哪个驱动、凭据从哪里来」，不构造驱动；装配在 cmd 层完成，
// 避免通知域反向依赖平台或 pkg 驱动的具体实现（CFG-07）。
package config

// 渠道驱动名。
const (
	// DriverMemory 进程内驱动，只记录不出网；仅 local / dev / test 允许。
	DriverMemory = "memory"
	// DriverDisabled 显式关闭渠道；生产未使用的渠道必须显式声明，
	// 避免「忘配」被误当作「已关闭」。
	DriverDisabled = "disabled"
	// DriverSMTP 标准 SMTP 邮件驱动。
	DriverSMTP = "smtp"
	// DriverAliyun 阿里云短信驱动。
	DriverAliyun = "aliyun"
	// DriverAPNS Apple Push Notification service 驱动。
	DriverAPNS = "apns"
	// DriverFCM Firebase Cloud Messaging 驱动。
	DriverFCM = "fcm"
)

// NotificationConfig 是通知域配置。
type NotificationConfig struct {
	// Channels 渠道路由配置，按 email / sms / push 三类。
	Channels ChannelsConfig `yaml:"channels"`
}

// ChannelsConfig 是三类渠道的配置集合。
type ChannelsConfig struct {
	// Email 邮件渠道。
	Email EmailConfig `yaml:"email"`
	// SMS 短信渠道。
	SMS SMSConfig `yaml:"sms"`
	// Push 移动推送渠道。
	Push PushConfig `yaml:"push"`
}

// EmailConfig 是邮件渠道配置。
//
// driver=memory 时不需要其余字段；driver=smtp 时 from / smtp.host /
// smtp.password_env 必填，密码本身只从环境变量读取。
type EmailConfig struct {
	// Driver memory | smtp | disabled。
	Driver string `yaml:"driver"`
	// From 默认发件地址，如 no-reply@example.com。
	From string `yaml:"from"`
	// FromName 发件人显示名。
	FromName string `yaml:"from_name"`
	// SMTP driver=smtp 时生效。
	SMTP SMTPConfig `yaml:"smtp"`
}

// SMTPConfig 是 SMTP 连接与认证配置。
type SMTPConfig struct {
	// Host SMTP 服务器主机名。
	Host string `yaml:"host"`
	// Port 端口；0 使用驱动默认 587。
	Port int `yaml:"port"`
	// Encryption starttls（默认）| tls | none。
	Encryption string `yaml:"encryption"`
	// Auth plain（默认）| login | none。
	Auth string `yaml:"auth"`
	// Username 认证用户名。
	Username string `yaml:"username"`
	// PasswordEnv 保存 SMTP 密码的环境变量名；禁止在 YAML 写明文密码。
	PasswordEnv string `yaml:"password_env"`
	// HELOName EHLO 名称；空则使用主机名。
	HELOName string `yaml:"helo_name"`
	// TLSMinVersion 1.2（默认）| 1.3。
	TLSMinVersion string `yaml:"tls_min_version"`
	// AllowInsecure 允许明文 SMTP 或无认证；生产禁止。
	AllowInsecure bool `yaml:"allow_insecure"`
}

// SMSConfig 是短信渠道配置。
//
// driver=aliyun 时 template_id / sign_name / access_key_id /
// access_key_secret_env 必填。
type SMSConfig struct {
	// Driver memory | aliyun | disabled。
	Driver string `yaml:"driver"`
	// TemplateID 厂商模板编码；通知模板渲染结果作为模板参数。
	TemplateID string `yaml:"template_id"`
	// SignName 默认短信签名。
	SignName string `yaml:"sign_name"`
	// Aliyun driver=aliyun 时生效。
	Aliyun AliyunConfig `yaml:"aliyun"`
}

// AliyunConfig 是阿里云短信（Dysmsapi）配置。
type AliyunConfig struct {
	// Endpoint 默认 https://dysmsapi.aliyuncs.com。
	Endpoint string `yaml:"endpoint"`
	// Region 默认 cn-hangzhou。
	Region string `yaml:"region"`
	// AccessKeyID 访问密钥 ID。
	AccessKeyID string `yaml:"access_key_id"`
	// AccessKeySecretEnv 保存 AccessKeySecret 的环境变量名。
	AccessKeySecretEnv string `yaml:"access_key_secret_env"`
	// SignName 厂商签名；为空时回退 SMSConfig.SignName。
	SignName string `yaml:"sign_name"`
}

// PushConfig 是移动推送渠道配置。
//
// driver=apns 时 platform 为 ios；driver=fcm 时为 android。
type PushConfig struct {
	// Driver memory | apns | fcm | disabled。
	Driver string `yaml:"driver"`
	// Platform ios | android；空时按驱动推导。
	Platform string `yaml:"platform"`
	// APNS driver=apns 时生效。
	APNS APNSConfig `yaml:"apns"`
	// FCM driver=fcm 时生效。
	FCM FCMConfig `yaml:"fcm"`
}

// APNSConfig 是 APNs Token 认证配置。
type APNSConfig struct {
	// Endpoint 默认生产地址；沙箱需显式覆盖。
	Endpoint string `yaml:"endpoint"`
	// Topic App Bundle ID。
	Topic string `yaml:"topic"`
	// KeyID APNs Auth Key 的 Key ID。
	KeyID string `yaml:"key_id"`
	// TeamID Apple Developer Team ID。
	TeamID string `yaml:"team_id"`
	// PrivateKeyEnv 保存 .p8 PEM 内容或文件路径的环境变量名。
	PrivateKeyEnv string `yaml:"private_key_env"`
	// PushType 空按 alert 处理。
	PushType string `yaml:"push_type"`
}

// FCMConfig 是 Firebase Cloud Messaging HTTP v1 配置。
type FCMConfig struct {
	// Endpoint 默认 https://fcm.googleapis.com。
	Endpoint string `yaml:"endpoint"`
	// ProjectID 空时使用 Service Account 中的 project_id。
	ProjectID string `yaml:"project_id"`
	// CredentialsJSONEnv 保存 Service Account JSON 内容或文件路径的环境变量名。
	CredentialsJSONEnv string `yaml:"credentials_json_env"`
}

// ApplyDefaults 填充与其它字段无关的默认值：未声明的渠道按 memory 处理，
// 推送平台按驱动推导。生产环境由 Validate 拒绝 memory。
func (c *NotificationConfig) ApplyDefaults() {
	if c.Channels.Email.Driver == "" {
		c.Channels.Email.Driver = DriverMemory
	}
	if c.Channels.SMS.Driver == "" {
		c.Channels.SMS.Driver = DriverMemory
	}
	if c.Channels.Push.Driver == "" {
		c.Channels.Push.Driver = DriverMemory
	}
	if c.Channels.Push.Platform == "" {
		switch c.Channels.Push.Driver {
		case DriverAPNS:
			c.Channels.Push.Platform = "ios"
		case DriverFCM:
			c.Channels.Push.Platform = "android"
		}
	}
}

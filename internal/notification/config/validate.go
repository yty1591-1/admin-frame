package config

import (
	"fmt"
	"strings"
)

// Validate 校验渠道驱动与凭据来源。
//
// 失败关闭：prod / staging 必须显式选择真实驱动或 disabled，不允许 memory
// 静默吞掉通知；凭据只校验「环境变量名」是否存在，值在装配期读取并再次
// 失败关闭，避免把密钥写进 YAML 或启动日志。
func (c NotificationConfig) Validate(env string) []error {
	var errs []error
	prodLike := isProdLike(env)

	errs = append(errs, validateEmail(c.Channels.Email, prodLike)...)
	errs = append(errs, validateSMS(c.Channels.SMS, prodLike)...)
	errs = append(errs, validatePush(c.Channels.Push, prodLike)...)
	return errs
}

func validateEmail(c EmailConfig, prodLike bool) []error {
	driver := driverOrDefault(c.Driver)
	switch driver {
	case DriverMemory:
		if prodLike {
			return []error{fmt.Errorf("notification.channels.email.driver=memory is forbidden when env is prod/staging; use smtp or disabled")}
		}
		return nil
	case DriverDisabled:
		return nil
	case DriverSMTP:
	default:
		return []error{fmt.Errorf("unsupported notification.channels.email.driver: %s", c.Driver)}
	}
	var errs []error
	if strings.TrimSpace(c.From) == "" {
		errs = append(errs, fmt.Errorf("notification.channels.email.from is required when driver=smtp"))
	}
	if strings.TrimSpace(c.SMTP.Host) == "" {
		errs = append(errs, fmt.Errorf("notification.channels.email.smtp.host is required when driver=smtp"))
	}
	if strings.TrimSpace(c.SMTP.PasswordEnv) == "" {
		errs = append(errs, fmt.Errorf("notification.channels.email.smtp.password_env is required when driver=smtp"))
	}
	if prodLike && c.SMTP.AllowInsecure {
		errs = append(errs, fmt.Errorf("notification.channels.email.smtp.allow_insecure is forbidden when env is prod/staging"))
	}
	if prodLike && strings.EqualFold(strings.TrimSpace(c.SMTP.Encryption), "none") {
		errs = append(errs, fmt.Errorf("notification.channels.email.smtp.encryption=none is forbidden when env is prod/staging"))
	}
	return errs
}

func validateSMS(c SMSConfig, prodLike bool) []error {
	driver := driverOrDefault(c.Driver)
	switch driver {
	case DriverMemory:
		if prodLike {
			return []error{fmt.Errorf("notification.channels.sms.driver=memory is forbidden when env is prod/staging; use aliyun or disabled")}
		}
		return nil
	case DriverDisabled:
		return nil
	case DriverAliyun:
	default:
		return []error{fmt.Errorf("unsupported notification.channels.sms.driver: %s", c.Driver)}
	}
	var errs []error
	if strings.TrimSpace(c.TemplateID) == "" {
		errs = append(errs, fmt.Errorf("notification.channels.sms.template_id is required when driver=aliyun"))
	}
	if strings.TrimSpace(c.SignName) == "" && strings.TrimSpace(c.Aliyun.SignName) == "" {
		errs = append(errs, fmt.Errorf("notification.channels.sms.sign_name is required when driver=aliyun"))
	}
	if strings.TrimSpace(c.Aliyun.AccessKeyID) == "" {
		errs = append(errs, fmt.Errorf("notification.channels.sms.aliyun.access_key_id is required when driver=aliyun"))
	}
	if strings.TrimSpace(c.Aliyun.AccessKeySecretEnv) == "" {
		errs = append(errs, fmt.Errorf("notification.channels.sms.aliyun.access_key_secret_env is required when driver=aliyun"))
	}
	return errs
}

func validatePush(c PushConfig, prodLike bool) []error {
	driver := driverOrDefault(c.Driver)
	switch driver {
	case DriverMemory:
		if prodLike {
			return []error{fmt.Errorf("notification.channels.push.driver=memory is forbidden when env is prod/staging; use apns/fcm or disabled")}
		}
		return nil
	case DriverDisabled:
		return nil
	case DriverAPNS, DriverFCM:
	default:
		return []error{fmt.Errorf("unsupported notification.channels.push.driver: %s", c.Driver)}
	}

	var errs []error
	platform := normalize(c.Platform)
	switch driver {
	case DriverAPNS:
		if platform != "" && platform != "ios" {
			errs = append(errs, fmt.Errorf("notification.channels.push.platform must be ios when driver=apns"))
		}
		if strings.TrimSpace(c.APNS.Topic) == "" {
			errs = append(errs, fmt.Errorf("notification.channels.push.apns.topic is required when driver=apns"))
		}
		if strings.TrimSpace(c.APNS.KeyID) == "" {
			errs = append(errs, fmt.Errorf("notification.channels.push.apns.key_id is required when driver=apns"))
		}
		if strings.TrimSpace(c.APNS.TeamID) == "" {
			errs = append(errs, fmt.Errorf("notification.channels.push.apns.team_id is required when driver=apns"))
		}
		if strings.TrimSpace(c.APNS.PrivateKeyEnv) == "" {
			errs = append(errs, fmt.Errorf("notification.channels.push.apns.private_key_env is required when driver=apns"))
		}
	case DriverFCM:
		if platform != "" && platform != "android" {
			errs = append(errs, fmt.Errorf("notification.channels.push.platform must be android when driver=fcm"))
		}
		if strings.TrimSpace(c.FCM.CredentialsJSONEnv) == "" {
			errs = append(errs, fmt.Errorf("notification.channels.push.fcm.credentials_json_env is required when driver=fcm"))
		}
	}
	return errs
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// driverOrDefault 把未配置的驱动视为 memory；生产环境随后会被拒绝。
func driverOrDefault(value string) string {
	driver := normalize(value)
	if driver == "" {
		return DriverMemory
	}
	return driver
}

func isProdLike(env string) bool {
	switch normalize(env) {
	case "staging", "prod":
		return true
	default:
		return false
	}
}

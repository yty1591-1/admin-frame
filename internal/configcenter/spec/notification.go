package spec

import "github.com/admin-frame/admin-frame/internal/configcenter/domain"

// notificationKeySpecs 返回通知域可迁移键声明。
//
// 排除项：SMTP 主机 / 端口 / 认证、阿里云 AK、APNs / FCM 凭据与端点随基础设施
// 与密钥托管部署，不进入普通配置键。
func notificationKeySpecs() []domain.KeySpec {
	keys := namespaceSpecs{namespace: NamespaceNotification, owner: ownerNotification}
	return []domain.KeySpec{
		keys.sensitiveAuto("notification.blacklist_salt",
			"通知黑名单 HMAC 盐（≥32 字节；密钥子域；首次自动生成）",
			&domain.SecretRule{MinLen: 32, Encoding: "raw", AutoGenerate: true}),
		keys.sensitiveAuto("notification.callback_secret",
			"通知供应商回调验签密钥（≥32 字节；密钥子域；首次自动生成）",
			&domain.SecretRule{MinLen: 32, Encoding: "raw", AutoGenerate: true}),
		keys.sensitiveAuto("notification.smtp.password",
			"SMTP 密码（密钥子域；prod-like 播种可占位生成，须轮换真值）",
			&domain.SecretRule{MinLen: 1, Encoding: "raw", AutoGenerate: false}),
		keys.sensitiveAuto("notification.sms.aliyun.access_key_id",
			"阿里云短信 AccessKeyId（密钥子域；prod-like 播种可占位生成，须轮换真值）",
			&domain.SecretRule{MinLen: 1, Encoding: "raw", AutoGenerate: false}),
		keys.sensitiveAuto("notification.sms.aliyun.access_key_secret",
			"阿里云短信 AccessKeySecret（密钥子域；prod-like 播种可占位生成，须轮换真值）",
			&domain.SecretRule{MinLen: 1, Encoding: "raw", AutoGenerate: false}),
		keys.sensitiveAuto("notification.push.apns.private_key",
			"APNs 私钥 PEM（密钥子域；prod-like 播种可占位生成，须轮换真值）",
			&domain.SecretRule{
				MinLen: 32, Encoding: "raw", AutoGenerate: false,
				Algorithm: domain.SecretAlgorithmECP256PEM,
			}),
		keys.sensitiveAuto("notification.push.fcm.credentials_json",
			"FCM 服务账号 JSON（密钥子域；prod-like 播种可占位生成，须轮换真值）",
			&domain.SecretRule{
				MinLen: 32, Encoding: "raw", AutoGenerate: false,
				Algorithm: domain.SecretAlgorithmFCMSAJSON,
			}),
		keys.enum("notification.channels.email.driver", "memory", "邮件渠道驱动",
			"memory", "smtp", "disabled"),
		keys.str("notification.channels.email.from", "", "默认发件地址"),
		keys.str("notification.channels.email.from_name", "", "发件人显示名"),
		keys.boolean("notification.channels.email.smtp.allow_insecure", false,
			"允许明文 SMTP 或无认证；生产禁止"),
		keys.enum("notification.channels.sms.driver", "memory", "短信渠道驱动",
			"memory", "aliyun", "disabled"),
		keys.str("notification.channels.sms.template_id", "", "短信通知模板编码"),
		keys.str("notification.channels.sms.sign_name", "", "短信默认签名"),
		keys.enum("notification.channels.push.driver", "memory", "移动推送驱动",
			"memory", "apns", "fcm", "disabled"),
		keys.str("notification.channels.push.platform", "", "推送平台；空时按驱动推导"),
	}
}

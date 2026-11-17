package secretruntime

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/admin-frame/admin-frame/pkg/secretclient"
)

const (
	notificationBlacklistSaltName  = "notification.blacklist_salt"
	notificationCallbackSecretName = "notification.callback_secret"
	// NotificationSMTPPasswordName 是 SMTP 密码的配置中心密钥名。
	NotificationSMTPPasswordName = "notification.smtp.password"
	// NotificationAliyunAccessKeyID 是通知域阿里云短信 AccessKeyId 密钥名。
	NotificationAliyunAccessKeyID = "notification.sms.aliyun.access_key_id"
	// NotificationAliyunAccessKeySecret 是通知域阿里云短信 AccessKeySecret 密钥名。
	NotificationAliyunAccessKeySecret = "notification.sms.aliyun.access_key_secret"
	// NotificationAliyunAccessKeyIDEnv 是 local/dev/test 回退用的 AccessKeyId 环境变量名。
	NotificationAliyunAccessKeyIDEnv = "ALIYUN_SMS_ACCESS_KEY_ID"
	// NotificationAPNSPrivateKeyName 是 APNs 私钥 PEM 的配置中心密钥名。
	NotificationAPNSPrivateKeyName = "notification.push.apns.private_key"
	// NotificationFCMCredentialsJSONName 是 FCM 服务账号 JSON 的配置中心密钥名。
	NotificationFCMCredentialsJSONName = "notification.push.fcm.credentials_json"
	notificationNamespace              = "notification"
)

// ResolveNotificationBlacklistSalt 从密钥子域解析通知黑名单盐（CC-R-05）。
func ResolveNotificationBlacklistSalt(ctx context.Context, env string, client *secretclient.Client) (string, error) {
	return resolveNotificationSecret(ctx, env, client, notificationBlacklistSaltName)
}

// ResolveNotificationBlacklistSaltFromEnv 构造 secretclient 并解析黑名单盐。
func ResolveNotificationBlacklistSaltFromEnv(ctx context.Context, env string) (string, error) {
	return resolveNotificationSecretFromEnv(ctx, env, notificationBlacklistSaltName)
}

// ResolveNotificationCallbackSecret 从密钥子域解析回调验签密钥（CC-R-05）。
func ResolveNotificationCallbackSecret(ctx context.Context, env string, client *secretclient.Client) (string, error) {
	return resolveNotificationSecret(ctx, env, client, notificationCallbackSecretName)
}

// ResolveNotificationCallbackSecretFromEnv 构造 secretclient 并解析回调验签密钥。
func ResolveNotificationCallbackSecretFromEnv(ctx context.Context, env string) (string, error) {
	return resolveNotificationSecretFromEnv(ctx, env, notificationCallbackSecretName)
}

// ResolveNotificationChannelSecretOrEnv 优先 secretclient，失败时仅 local/dev/test 回退裸 env。
func ResolveNotificationChannelSecretOrEnv(ctx context.Context, env, secretName, envVar string) (string, error) {
	value, err := resolveNotificationSecretFromEnv(ctx, env, secretName)
	if err == nil {
		return value, nil
	}
	if bareErr := RejectBareSecretEnv(env, envVar); bareErr != nil {
		return "", fmt.Errorf("%s: resolve failed (%v); %w", secretName, err, bareErr)
	}
	fallback := strings.TrimSpace(os.Getenv(strings.TrimSpace(envVar)))
	if fallback == "" {
		return "", fmt.Errorf("%s: resolve failed (%v) and environment variable %s is empty",
			secretName, err, envVar)
	}
	return fallback, nil
}

func resolveNotificationSecretFromEnv(ctx context.Context, env, name string) (string, error) {
	client, err := NewFromEnv()
	if err != nil {
		return "", err
	}
	return resolveNotificationSecret(ctx, env, client, name)
}

func resolveNotificationSecret(ctx context.Context, env string, client *secretclient.Client, name string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("secretruntime: secretclient is required for %s", name)
	}
	resolvedEnv := strings.TrimSpace(env)
	if resolvedEnv == "" {
		return "", fmt.Errorf("secretruntime: env is required to resolve %s", name)
	}
	value, err := client.Resolve(ctx, resolvedEnv, notificationNamespace, name)
	if err != nil {
		return "", fmt.Errorf("secretruntime: resolve %s: %w", name, err)
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("secretruntime: %s resolved empty", name)
	}
	return value, nil
}

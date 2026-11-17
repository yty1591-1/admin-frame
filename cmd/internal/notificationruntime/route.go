package notificationruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/admin-frame/admin-frame/cmd/internal/secretruntime"
	"github.com/admin-frame/admin-frame/internal/notification/app"
	notificationconfig "github.com/admin-frame/admin-frame/internal/notification/config"
	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/notification/infrastructure/channel"
	pkgemail "github.com/admin-frame/admin-frame/pkg/email"
	pkgpush "github.com/admin-frame/admin-frame/pkg/push"
	pkgsms "github.com/admin-frame/admin-frame/pkg/sms"

	// 渠道驱动注册：通知域通过统一 Opener 接口按配置装配。
	_ "github.com/admin-frame/admin-frame/pkg/email/smtp"
	_ "github.com/admin-frame/admin-frame/pkg/push/apns"
	_ "github.com/admin-frame/admin-frame/pkg/push/fcm"
	_ "github.com/admin-frame/admin-frame/pkg/sms/aliyun"
)

// buildRoute 按通知渠道配置构造渠道路由（ENT-10 NT-04）。
//
// 配置校验已在启动期完成；这里只做凭据读取与驱动构造，任一渠道配置了
// 真实驱动但凭据缺失时直接失败，避免「进程起来了但通知永远发不出去」。
// staging/prod 拒绝裸 env 凭据（CC-R-03）。
func buildRoute(ctx context.Context, env string, cfg notificationconfig.NotificationConfig) (map[domain.Channel][]app.Sender, error) {
	route := make(map[domain.Channel][]app.Sender, 3)

	emailSenders, err := buildEmailSenders(ctx, env, cfg.Channels.Email)
	if err != nil {
		return nil, err
	}
	if len(emailSenders) > 0 {
		route[domain.ChannelEmail] = emailSenders
	}

	smsSenders, err := buildSMSSenders(ctx, env, cfg.Channels.SMS)
	if err != nil {
		return nil, err
	}
	if len(smsSenders) > 0 {
		route[domain.ChannelSms] = smsSenders
	}

	pushSenders, err := buildPushSenders(ctx, env, cfg.Channels.Push)
	if err != nil {
		return nil, err
	}
	if len(pushSenders) > 0 {
		route[domain.ChannelPush] = pushSenders
	}
	return route, nil
}

func buildEmailSenders(ctx context.Context, env string, cfg notificationconfig.EmailConfig) ([]app.Sender, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case notificationconfig.DriverDisabled:
		return nil, nil
	case notificationconfig.DriverMemory, "":
		return []app.Sender{
			channel.NewEmailSender(pkgemail.NewMemory(pkgemail.MemoryConfig{}), pkgemail.Address{}),
		}, nil
	case notificationconfig.DriverSMTP:
		password, err := secretruntime.ResolveNotificationChannelSecretOrEnv(ctx, env,
			secretruntime.NotificationSMTPPasswordName, cfg.SMTP.PasswordEnv)
		if err != nil {
			return nil, fmt.Errorf("notification email: smtp password: %w", err)
		}
		sender, err := pkgemail.Open(ctx, pkgemail.Config{
			Driver: notificationconfig.DriverSMTP,
			From:   pkgemail.Address{Name: cfg.FromName, Email: cfg.From},
			SMTP: pkgemail.SMTPConfig{
				Host:          cfg.SMTP.Host,
				Port:          cfg.SMTP.Port,
				Encryption:    cfg.SMTP.Encryption,
				Auth:          cfg.SMTP.Auth,
				Username:      cfg.SMTP.Username,
				Password:      password,
				HELOName:      cfg.SMTP.HELOName,
				TLSMinVersion: cfg.SMTP.TLSMinVersion,
				AllowInsecure: cfg.SMTP.AllowInsecure,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("notification email: open smtp driver: %w", err)
		}
		return []app.Sender{
			channel.NewEmailSender(sender, pkgemail.Address{Name: cfg.FromName, Email: cfg.From}),
		}, nil
	default:
		return nil, fmt.Errorf("notification email: unsupported driver %q", cfg.Driver)
	}
}

func buildSMSSenders(ctx context.Context, env string, cfg notificationconfig.SMSConfig) ([]app.Sender, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case notificationconfig.DriverDisabled:
		return nil, nil
	case notificationconfig.DriverMemory, "":
		// memory 驱动按裸内容记录，方便本地查看渲染结果。
		return []app.Sender{
			channel.NewSMSSender(pkgsms.NewMemory(pkgsms.MemoryConfig{AllowRawContent: true}), "", ""),
		}, nil
	case notificationconfig.DriverAliyun:
		secret, err := secretruntime.ResolveNotificationChannelSecretOrEnv(ctx, env,
			secretruntime.NotificationAliyunAccessKeySecret, cfg.Aliyun.AccessKeySecretEnv)
		if err != nil {
			return nil, fmt.Errorf("notification sms: aliyun access key secret: %w", err)
		}
		signName := strings.TrimSpace(cfg.SignName)
		if signName == "" {
			signName = strings.TrimSpace(cfg.Aliyun.SignName)
		}
		sender, err := pkgsms.Open(ctx, pkgsms.Config{
			Driver: notificationconfig.DriverAliyun,
			Aliyun: pkgsms.AliyunConfig{
				Endpoint:        cfg.Aliyun.Endpoint,
				Region:          cfg.Aliyun.Region,
				AccessKeyID:     cfg.Aliyun.AccessKeyID,
				AccessKeySecret: secret,
				SignName:        signName,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("notification sms: open aliyun driver: %w", err)
		}
		return []app.Sender{
			channel.NewSMSSender(sender, signName, cfg.TemplateID),
		}, nil
	default:
		return nil, fmt.Errorf("notification sms: unsupported driver %q", cfg.Driver)
	}
}

func buildPushSenders(ctx context.Context, env string, cfg notificationconfig.PushConfig) ([]app.Sender, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	platform, err := pushPlatform(driver, cfg.Platform)
	if err != nil {
		return nil, err
	}
	switch driver {
	case notificationconfig.DriverDisabled:
		return nil, nil
	case notificationconfig.DriverMemory, "":
		return []app.Sender{
			channel.NewPushSender(pkgpush.NewMemory(pkgpush.MemoryConfig{}), platform),
		}, nil
	case notificationconfig.DriverAPNS:
		privateKey, err := secretruntime.ResolveNotificationChannelSecretOrEnv(ctx, env,
			secretruntime.NotificationAPNSPrivateKeyName, cfg.APNS.PrivateKeyEnv)
		if err != nil {
			return nil, fmt.Errorf("notification push: apns private key: %w", err)
		}
		sender, openErr := pkgpush.Open(ctx, pkgpush.Config{
			Driver: notificationconfig.DriverAPNS,
			APNS: pkgpush.APNSConfig{
				Endpoint:   cfg.APNS.Endpoint,
				Topic:      cfg.APNS.Topic,
				KeyID:      cfg.APNS.KeyID,
				TeamID:     cfg.APNS.TeamID,
				PrivateKey: privateKey,
				PushType:   cfg.APNS.PushType,
			},
		})
		if openErr != nil {
			return nil, fmt.Errorf("notification push: open apns driver: %w", openErr)
		}
		return []app.Sender{channel.NewPushSender(sender, platform)}, nil
	case notificationconfig.DriverFCM:
		credentials, err := secretruntime.ResolveNotificationChannelSecretOrEnv(ctx, env,
			secretruntime.NotificationFCMCredentialsJSONName, cfg.FCM.CredentialsJSONEnv)
		if err != nil {
			return nil, fmt.Errorf("notification push: fcm credentials: %w", err)
		}
		sender, openErr := pkgpush.Open(ctx, pkgpush.Config{
			Driver: notificationconfig.DriverFCM,
			FCM: pkgpush.FCMConfig{
				Endpoint:        cfg.FCM.Endpoint,
				ProjectID:       cfg.FCM.ProjectID,
				CredentialsJSON: credentials,
			},
		})
		if openErr != nil {
			return nil, fmt.Errorf("notification push: open fcm driver: %w", openErr)
		}
		return []app.Sender{channel.NewPushSender(sender, platform)}, nil
	default:
		return nil, fmt.Errorf("notification push: unsupported driver %q", cfg.Driver)
	}
}

func pushPlatform(driver, configured string) (pkgpush.Platform, error) {
	platform := strings.ToLower(strings.TrimSpace(configured))
	if platform == "" {
		switch driver {
		case notificationconfig.DriverAPNS:
			return pkgpush.PlatformIOS, nil
		case notificationconfig.DriverFCM:
			return pkgpush.PlatformAndroid, nil
		default:
			return pkgpush.PlatformIOS, nil
		}
	}
	value := pkgpush.Platform(platform)
	if !value.Valid() {
		return "", fmt.Errorf("notification push: unsupported platform %q", configured)
	}
	return value, nil
}

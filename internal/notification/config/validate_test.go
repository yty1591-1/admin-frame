package config

import (
	"strings"
	"testing"
)

func TestValidateDefaultsMemoryAllowedOnlyOutsideProd(t *testing.T) {
	var cfg NotificationConfig
	cfg.ApplyDefaults()

	if errs := cfg.Validate("dev"); len(errs) != 0 {
		t.Fatalf("dev memory defaults: unexpected errors: %v", errs)
	}
	errs := cfg.Validate("prod")
	if len(errs) == 0 {
		t.Fatal("prod memory defaults: want errors, got none")
	}
	for _, err := range errs {
		if !strings.Contains(err.Error(), "memory is forbidden") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestValidateSMTP(t *testing.T) {
	cfg := NotificationConfig{Channels: ChannelsConfig{
		Email: EmailConfig{
			Driver: DriverSMTP,
			From:   "no-reply@example.com",
			SMTP: SMTPConfig{
				Host:        "smtp.example.com",
				PasswordEnv: "SMTP_PASSWORD",
			},
		},
		SMS:  SMSConfig{Driver: DriverDisabled},
		Push: PushConfig{Driver: DriverDisabled},
	}}
	if errs := cfg.Validate("prod"); len(errs) != 0 {
		t.Fatalf("valid smtp config: unexpected errors: %v", errs)
	}

	cfg.Channels.Email.SMTP.PasswordEnv = ""
	if errs := cfg.Validate("prod"); len(errs) == 0 {
		t.Fatal("missing password_env: want error, got none")
	}
}

func TestValidateDisabledPerChannel(t *testing.T) {
	cfg := NotificationConfig{Channels: ChannelsConfig{
		Email: EmailConfig{Driver: DriverDisabled},
		SMS:   SMSConfig{Driver: DriverDisabled},
		Push:  PushConfig{Driver: DriverDisabled},
	}}
	if errs := cfg.Validate("prod"); len(errs) != 0 {
		t.Fatalf("disabled channels in prod: unexpected errors: %v", errs)
	}
}

func TestValidateAliyunAndAPNS(t *testing.T) {
	cfg := NotificationConfig{Channels: ChannelsConfig{
		Email: EmailConfig{Driver: DriverDisabled},
		SMS: SMSConfig{
			Driver:     DriverAliyun,
			TemplateID: "SMS_123",
			SignName:   "AdminFrame",
			Aliyun: AliyunConfig{ //nolint:gosec // G101: 测试占位值，非真实凭据
				AccessKeyID:        "ak",
				AccessKeySecretEnv: "ALIYUN_SMS_SECRET",
			},
		},
		Push: PushConfig{
			Driver: DriverAPNS,
			APNS: APNSConfig{
				Topic: "com.example.app", KeyID: "KEY", TeamID: "TEAM",
				PrivateKeyEnv: "APNS_PRIVATE_KEY",
			},
			Platform: "ios",
		},
	}}
	if errs := cfg.Validate("prod"); len(errs) != 0 {
		t.Fatalf("valid aliyun/apns config: unexpected errors: %v", errs)
	}

	cfg.Channels.Push.Platform = "android"
	if errs := cfg.Validate("prod"); len(errs) == 0 {
		t.Fatal("apns with android platform: want error, got none")
	}
}

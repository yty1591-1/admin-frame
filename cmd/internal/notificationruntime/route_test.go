package notificationruntime

import (
	"context"
	"strings"
	"testing"

	notificationconfig "github.com/admin-frame/admin-frame/internal/notification/config"
	"github.com/admin-frame/admin-frame/internal/notification/domain"
)

func TestBuildRouteMemory(t *testing.T) {
	cfg := notificationconfig.NotificationConfig{}
	cfg.ApplyDefaults()

	route, err := buildRoute(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build route: %v", err)
	}
	for _, ch := range []domain.Channel{domain.ChannelEmail, domain.ChannelSms, domain.ChannelPush} {
		if len(route[ch]) == 0 {
			t.Fatalf("channel %s: want memory sender, got none", ch)
		}
	}
}

func TestBuildRouteDisabled(t *testing.T) {
	cfg := notificationconfig.NotificationConfig{Channels: notificationconfig.ChannelsConfig{
		Email: notificationconfig.EmailConfig{Driver: notificationconfig.DriverDisabled},
		SMS:   notificationconfig.SMSConfig{Driver: notificationconfig.DriverDisabled},
		Push:  notificationconfig.PushConfig{Driver: notificationconfig.DriverDisabled},
	}}
	route, err := buildRoute(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build route: %v", err)
	}
	if len(route) != 0 {
		t.Fatalf("disabled channels: want empty route, got %d", len(route))
	}
}

func TestBuildRouteMissingSecret(t *testing.T) {
	cfg := notificationconfig.NotificationConfig{Channels: notificationconfig.ChannelsConfig{
		Email: notificationconfig.EmailConfig{
			Driver: notificationconfig.DriverSMTP,
			From:   "no-reply@example.com",
			SMTP: notificationconfig.SMTPConfig{
				Host:        "smtp.example.com",
				PasswordEnv: "ADMIN_FRAME_TEST_MISSING_SMTP_PASSWORD",
			},
		},
	}}
	_, err := buildRoute(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "ADMIN_FRAME_TEST_MISSING_SMTP_PASSWORD") {
		t.Fatalf("want missing secret error, got %v", err)
	}
}

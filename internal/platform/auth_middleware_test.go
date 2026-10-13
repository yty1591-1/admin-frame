package platform_test

import (
	"testing"

	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

func TestNewAuthMiddlewareRejectsProductionWithoutAuthenticator(t *testing.T) {
	_, err := platform.NewAuthMiddleware(platform.AuthConfig{DemoMode: false, ExpectedChannel: "c"}, auth.ChannelC)
	if err == nil {
		t.Fatal("expected error when demo_mode is false")
	}
}

func TestNewAuthMiddlewareRejectsChannelMismatch(t *testing.T) {
	_, err := platform.NewAuthMiddleware(platform.AuthConfig{DemoMode: true, ExpectedChannel: "admin"}, auth.ChannelC)
	if err == nil {
		t.Fatal("expected channel mismatch")
	}
}

func TestNewAuthMiddlewareDemo(t *testing.T) {
	mw, err := platform.NewAuthMiddleware(platform.AuthConfig{DemoMode: true, ExpectedChannel: "c"}, auth.ChannelC)
	if err != nil {
		t.Fatal(err)
	}
	if mw == nil {
		t.Fatal("nil middleware")
	}
}

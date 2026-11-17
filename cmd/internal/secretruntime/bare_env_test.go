package secretruntime

import "testing"

func TestRejectBareSecretEnv(t *testing.T) {
	t.Parallel()
	if err := RejectBareSecretEnv("local", "ALIYUN_SMS_ACCESS_KEY_SECRET"); err != nil {
		t.Fatalf("local must allow: %v", err)
	}
	if err := RejectBareSecretEnv("dev", "x"); err != nil {
		t.Fatalf("dev must allow: %v", err)
	}
	if err := RejectBareSecretEnv("prod", "ALIYUN_SMS_ACCESS_KEY_SECRET"); err == nil {
		t.Fatal("prod must reject bare env secrets")
	}
	if err := RejectBareSecretEnv("staging", "ADMIN_FRAME_NOTIFICATION_BLACKLIST_SALT"); err == nil {
		t.Fatal("staging must reject bare env secrets")
	}
}

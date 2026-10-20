package webhook_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/webhook"
)

func TestDecodeSecret(t *testing.T) {
	raw := testKey(0x2a)
	encoded := base64.StdEncoding.EncodeToString(raw)
	unpadded := strings.TrimRight(encoded, "=")

	valid := []struct {
		name  string
		input string
	}{
		{name: "base64", input: encoded},
		{name: "prefixed", input: "whsec_" + encoded},
		{name: "unpadded", input: unpadded},
		{name: "prefixed unpadded", input: "whsec_" + unpadded},
		{name: "surrounding spaces", input: "  " + encoded + "\n"},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			decoded, err := webhook.DecodeSecret(tt.input)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !bytes.Equal(decoded, raw) {
				t.Fatalf("decoded=%x want=%x", decoded, raw)
			}
		})
	}

	invalid := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "prefix only", input: "whsec_"},
		{name: "not base64", input: "not a secret!"},
		{name: "too short", input: base64.StdEncoding.EncodeToString(make([]byte, webhook.MinKeyBytes-1))},
		{name: "too long", input: base64.StdEncoding.EncodeToString(make([]byte, 4097))},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := webhook.DecodeSecret(tt.input); !errors.Is(err, webhook.ErrInvalidConfig) {
				t.Fatalf("err=%v want ErrInvalidConfig", err)
			}
		})
	}
}

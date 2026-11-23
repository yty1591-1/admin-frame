package spec_test

import (
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

func TestNamespaceTitleOrdering(t *testing.T) {
	cases := map[string]struct {
		title string
		order int
	}{
		"platform":     {title: "平台与基础设施", order: 10},
		"identity":     {title: "身份与登录", order: 30},
		"notification": {title: "通知通道", order: 60},
	}
	for namespace, want := range cases {
		title, order := spec.NamespaceTitle(namespace)
		if title != want.title || order != want.order {
			t.Fatalf("%s: title=%q order=%d", namespace, title, order)
		}
	}
	// 未登记的命名空间回退为自身名字，且排在最后。
	title, order := spec.NamespaceTitle("custom-domain")
	if title != "custom-domain" || order != 900 {
		t.Fatalf("fallback: title=%q order=%d", title, order)
	}
}

func TestGroupOfSecondLevel(t *testing.T) {
	sms := spec.GroupOf("identity.sms.access_key_id")
	if sms.ID != "identity.sms" || sms.Title == "" {
		t.Fatalf("sms group=%+v", sms)
	}
	channels := spec.GroupOf("notification.channels.sms.driver")
	if channels.ID != "notification.channels" {
		t.Fatalf("channels group=%+v", channels)
	}
	top := spec.GroupOf("db.driver")
	if top.ID != "db" || top.Order != 10 {
		t.Fatalf("db group=%+v", top)
	}
	other := spec.GroupOf("mystery.key")
	if other.ID != "other" {
		t.Fatalf("unknown group=%+v", other)
	}
}

func TestDeriveLabelAndWidget(t *testing.T) {
	label := spec.DeriveLabel(domain.KeySpec{
		Key: "http.timeout", Description: "HTTP 超时（秒级，影响所有出站请求）",
	})
	if label != "HTTP 超时" {
		t.Fatalf("label=%q", label)
	}
	if got := spec.DeriveLabel(domain.KeySpec{Key: "x"}); got != "x" {
		t.Fatalf("无描述时应回退键名，实际 %q", got)
	}
	cases := []struct {
		keySpec domain.KeySpec
		widget  string
	}{
		{keySpec: domain.KeySpec{ValueType: domain.ValueTypeString, Sensitive: true}, widget: "password"},
		{keySpec: domain.KeySpec{ValueType: domain.ValueTypeString, Enum: []string{"a", "b"}}, widget: "select"},
		{keySpec: domain.KeySpec{ValueType: domain.ValueTypeBool}, widget: "switch"},
		{keySpec: domain.KeySpec{ValueType: domain.ValueTypeInt}, widget: "number"},
		{keySpec: domain.KeySpec{ValueType: domain.ValueTypeDuration}, widget: "duration"},
		{keySpec: domain.KeySpec{ValueType: domain.ValueTypeJSON}, widget: "code"},
		{keySpec: domain.KeySpec{ValueType: domain.ValueTypeString}, widget: "text"},
	}
	for _, tc := range cases {
		if got := spec.WidgetFor(tc.keySpec); got != tc.widget {
			t.Fatalf("%s: widget=%q want %q", tc.keySpec.ValueType, got, tc.widget)
		}
	}
}

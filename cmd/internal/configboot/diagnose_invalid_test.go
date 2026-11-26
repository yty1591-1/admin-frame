package configboot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDiagnoseInvalidValuesFindsNamedKey 验证错误文案点名键时精确定位，且只重置该键。
func TestDiagnoseInvalidValuesFindsNamedKey(t *testing.T) {
	validate := func(values map[string]string) error {
		if values["db.driver"] == "bogus" {
			return errors.New(`config: invalid config: config validation failed: db.driver: unsupported value "bogus"`)
		}
		return nil
	}
	refs, ok := DiagnoseInvalidValues(map[string]string{
		"db.driver": "bogus", "log.level": "info",
	}, validate)
	if !ok {
		t.Fatal("want diagnosed")
	}
	if len(refs) != 1 || refs[0] != "platform/db.driver" {
		t.Fatalf("refs = %v, want [platform/db.driver]", refs)
	}
}

// TestDiagnoseInvalidValuesFallsBackToSingleScan 验证错误文案不含键名时逐键试探仍能定位。
func TestDiagnoseInvalidValuesFallsBackToSingleScan(t *testing.T) {
	validate := func(values map[string]string) error {
		if values["storage.driver"] == "bogus" {
			return errors.New("config: invalid config: unsupported storage settings")
		}
		return nil
	}
	refs, ok := DiagnoseInvalidValues(map[string]string{"storage.driver": "bogus"}, validate)
	if !ok {
		t.Fatal("want diagnosed")
	}
	if len(refs) != 1 || refs[0] != "platform/storage.driver" {
		t.Fatalf("refs = %v, want [platform/storage.driver]", refs)
	}
}

// TestDiagnoseInvalidValuesStaysConservative 验证多键同时非法且文案无键名时不猜、不降级。
func TestDiagnoseInvalidValuesStaysConservative(t *testing.T) {
	validate := func(values map[string]string) error {
		bad := values["db.driver"] == "bogus" || values["storage.driver"] == "bogus"
		if bad {
			return errors.New("config: invalid config: config validation failed")
		}
		return nil
	}
	refs, ok := DiagnoseInvalidValues(map[string]string{
		"db.driver": "bogus", "storage.driver": "bogus",
	}, validate)
	if ok {
		t.Fatalf("refs = %v, want no diagnosis", refs)
	}
}

// TestResetInvalidValuesPostsToConfigAPI 验证重置请求的路径、身份头与请求体。
func TestResetInvalidValuesPostsToConfigAPI(t *testing.T) {
	var gotPath, gotIdentity, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		gotIdentity = r.Header.Get("X-Service-Identity")
		payload := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(payload)
		gotBody = string(payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok"}`))
	}))
	defer server.Close()

	if err := ResetInvalidValues(context.Background(), server.URL, server.Client(),
		"admin-api", "prod", []string{"platform/db.driver"}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if gotPath != "POST /internal/v1/configs/repair/reset-defaults" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotIdentity != "admin-api" {
		t.Fatalf("identity header = %q", gotIdentity)
	}
	var body struct {
		Env  string   `json:"env"`
		Refs []string `json:"refs"`
	}
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("body %q: %v", gotBody, err)
	}
	if body.Env != "prod" || len(body.Refs) != 1 || body.Refs[0] != "platform/db.driver" {
		t.Fatalf("body = %+v", body)
	}
}

// TestResetInvalidValuesReportsServerRejection 验证非零业务码会被回报为错误。
func TestResetInvalidValuesReportsServerRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":40000,"message":"refs is required"}`))
	}))
	defer server.Close()

	err := ResetInvalidValues(context.Background(), server.URL, server.Client(),
		"", "dev", []string{"platform/db.driver"})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "code=40000") || !strings.Contains(err.Error(), "refs is required") {
		t.Fatalf("err = %v", err)
	}
}

package secretruntime

import "testing"

func TestLoadServerTLSFromEnvRequiresMTLSInProdLike(t *testing.T) {
	t.Setenv("CONFIG_API_TLS_CERT", "")
	t.Setenv("CONFIG_API_TLS_KEY", "")
	t.Setenv("CONFIG_API_TLS_CLIENT_CA", "")
	t.Setenv("CONFIG_API_MTLS", "off")

	_, err := LoadServerTLSFromEnvFor("prod")
	if err == nil {
		t.Fatal("prod + mTLS=off must fail")
	}
	_, err = LoadServerTLSFromEnvFor("staging")
	if err == nil {
		t.Fatal("staging + mTLS=off must fail")
	}
	_, err = LoadServerTLSFromEnvFor("local")
	if err != nil {
		t.Fatalf("local plaintext ok: %v", err)
	}
}

func TestLoadServerTLSFromEnvRequireNeedsCA(t *testing.T) {
	t.Setenv("CONFIG_API_TLS_CERT", "server.crt")
	t.Setenv("CONFIG_API_TLS_KEY", "server.key")
	t.Setenv("CONFIG_API_MTLS", "require")
	t.Setenv("CONFIG_API_TLS_CLIENT_CA", "")
	_, err := LoadServerTLSFromEnvFor("prod")
	if err == nil {
		t.Fatal("require without CA must fail")
	}
}

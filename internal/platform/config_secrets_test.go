package platform_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/platform"
)

// secretsBaseConfig 是密钥用例的公共头部：持久库 + 固定 JWT 基线。
const secretsBaseConfig = "service: admin-api\nchannel: admin\naddr: \":8081\"\n" +
	"auth:\n  jwt:\n    issuer: https://iam.example.com\n    audience: [admin]\n"

// yamlDir 把目录路径转成 YAML 双引号字符串的安全形式。
//
// Windows 路径含反斜杠，直接插入双引号会触发 YAML 转义解析错误，
// 因此必须转义为 `\\`。
func yamlDir(dir string) string {
	return `"` + strings.ReplaceAll(dir, `\`, `\\`) + `"`
}

// writeSecretFile 在 dir 下写一个密钥文件（模拟 K8s Secret 挂载）。
func writeSecretFile(t *testing.T, dir, name, value string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", dir, err)
	}
	// 挂载文件通常带尾随换行，FileProvider 需去掉。
	if err := os.WriteFile(filepath.Join(dir, name), []byte(value+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", name, err)
	}
}

// TestSecretsDisabledKeepsEnvInjection 验证未配置 providers 时完全沿用既有路径：
// 密钥仍由 ADMIN_FRAME_* 环境变量注入，加载结果不受 Provider 影响。
func TestSecretsDisabledKeepsEnvInjection(t *testing.T) {
	const dsn = "svc:pass@tcp(127.0.0.1:3306)/admin_frame"
	t.Setenv("ADMIN_FRAME_DB_DSN", dsn)

	path := writeConfig(t, "secrets-off.yaml", secretsBaseConfig+"db:\n  driver: mysql\n")
	cfg, err := platform.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.DB.DSN != dsn {
		t.Fatalf("DB.DSN = %q, want env value", cfg.DB.DSN)
	}
}

// TestSecretsEnvProviderUsesDefaultPrefix 验证默认 env 前缀带分隔符：
// identity_hash_key 必须命中 ADMIN_FRAME_IDENTITY_HASH_KEY，
// 而不是前缀与密钥名粘连的变量名。
func TestSecretsEnvProviderUsesDefaultPrefix(t *testing.T) {
	hashKey := strings.Repeat("ab", 32)
	t.Setenv("ADMIN_FRAME_IDENTITY_HASH_KEY", hashKey)

	yaml := "service: admin-api\nchannel: admin\naddr: \":8081\"\n" +
		"auth:\n  mode: jwt\n  jwt:\n    issuer: https://iam.example.com\n    audience: [admin]\n    secret: \"" + strings.Repeat("s", 32) + "\"\n" +
		"db:\n  driver: memory\n" +
		"redis:\n  addr: \"127.0.0.1:6379\"\n" +
		"identity:\n  enabled: true\n" +
		"secrets:\n  providers: [env]\n"
	cfg, err := platform.LoadConfig(writeConfig(t, "secrets-env-prefix.yaml", yaml))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Identity.HashKey != hashKey {
		t.Fatalf("identity.hash_key 未命中 ADMIN_FRAME_ 前缀变量")
	}
}

// TestSecretsFileOverridesConfig 验证托管源覆盖配置文件明文：
// file 命中即成为唯一真相，配置里写的旧值必须被替换。
func TestSecretsFileOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	writeSecretFile(t, dir, platform.SecretDBDSN, "svc:from-file@tcp(db:3306)/app")
	writeSecretFile(t, dir, platform.SecretAuthJWTSecret, strings.Repeat("k", 32))
	writeSecretFile(t, dir, platform.SecretRedisPassword, "redis-from-file")

	yaml := "service: admin-api\nchannel: admin\naddr: \":8081\"\n" +
		"auth:\n  jwt:\n    issuer: https://iam.example.com\n    audience: [admin]\n" +
		"    secret: \"" + strings.Repeat("c", 32) + "\"\n" +
		"db:\n  driver: mysql\n  dsn: \"svc:from-config@tcp(db:3306)/app\"\n" +
		"redis:\n  addr: \"127.0.0.1:6379\"\n  password: \"redis-from-config\"\n" +
		"secrets:\n  providers: [file]\n  file:\n    dir: " + yamlDir(dir) + "\n"
	cfg, err := platform.LoadConfig(writeConfig(t, "secrets-file.yaml", yaml))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.DB.DSN != "svc:from-file@tcp(db:3306)/app" {
		t.Fatalf("DB.DSN = %q, want file value", cfg.DB.DSN)
	}
	if cfg.Auth.JWT.Secret != strings.Repeat("k", 32) {
		t.Fatalf("auth.jwt.secret 未被托管源覆盖")
	}
	if cfg.Redis.Password != "redis-from-file" {
		t.Fatalf("redis.password = %q, want file value", cfg.Redis.Password)
	}
}

// TestSecretsOverridesIdentityKeys 验证身份域两个密钥同样走托管源。
//
// 身份域开启后会引入 auth.mode / redis 等前置校验，本用例据实配齐，
// 以确认「覆盖发生在 Validate 之前」——否则托管源补的密钥救不了缺字段。
func TestSecretsOverridesIdentityKeys(t *testing.T) {
	dir := t.TempDir()
	hashKey := strings.Repeat("ab", 32) // hex 编码 32 字节
	mfaKey := strings.Repeat("cd", 32)  // hex 编码 32 字节
	writeSecretFile(t, dir, platform.SecretIdentityHashKey, hashKey)
	writeSecretFile(t, dir, platform.SecretIdentityMFASecretKey, mfaKey)

	yaml := "service: admin-api\nchannel: admin\naddr: \":8081\"\n" +
		"auth:\n  mode: jwt\n  jwt:\n    issuer: https://iam.example.com\n    audience: [admin]\n    secret: \"" + strings.Repeat("s", 32) + "\"\n" +
		"db:\n  driver: memory\n" +
		"redis:\n  addr: \"127.0.0.1:6379\"\n" +
		"identity:\n  enabled: true\n" +
		"secrets:\n  providers: [file]\n  file:\n    dir: " + yamlDir(dir) + "\n"
	cfg, err := platform.LoadConfig(writeConfig(t, "secrets-identity.yaml", yaml))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Identity.HashKey != hashKey {
		t.Fatalf("identity.hash_key 未被托管源覆盖")
	}
	if cfg.Identity.MFA.SecretKey != mfaKey {
		t.Fatalf("identity.mfa.secret_key 未被托管源覆盖")
	}
}

// TestSecretsFileSatisfiesRequiredFields 验证生产实际形态：
// 配置文件里 dsn / jwt.secret 留空，全部由托管源提供，仍能通过启动校验。
//
// 这条是「托管源能独立满足必填项」的证明——否则迁移时必然出现
// 「文件已挂载但配置仍要写明文」的矛盾。
func TestSecretsFileSatisfiesRequiredFields(t *testing.T) {
	dir := t.TempDir()
	writeSecretFile(t, dir, platform.SecretDBDSN, "svc:file-only@tcp(db:3306)/app")
	writeSecretFile(t, dir, platform.SecretAuthJWTSecret, strings.Repeat("k", 32))

	yaml := "service: admin-api\nchannel: admin\naddr: \":8081\"\n" +
		"auth:\n  mode: jwt\n  jwt:\n    issuer: https://iam.example.com\n    audience: [admin]\n" +
		"db:\n  driver: mysql\n" + // dsn 故意留空
		"secrets:\n  providers: [file]\n  file:\n    dir: " + yamlDir(dir) + "\n"
	cfg, err := platform.LoadConfig(writeConfig(t, "secrets-required.yaml", yaml))
	if err != nil {
		t.Fatalf("配置留空 + 托管源提供密钥应当可启动，err = %v", err)
	}
	if cfg.DB.DSN != "svc:file-only@tcp(db:3306)/app" {
		t.Fatalf("DB.DSN = %q, want file value", cfg.DB.DSN)
	}
	if cfg.Auth.JWT.Secret != strings.Repeat("k", 32) {
		t.Fatalf("auth.jwt.secret 未由托管源补齐")
	}
}

// TestSecretsFileMissingKeepsConfiguredValue 验证未托管的密钥保持原值：
// 缺文件不应清空配置，是否致命交由既有字段校验判断。
func TestSecretsFileMissingKeepsConfiguredValue(t *testing.T) {
	dir := t.TempDir()
	writeSecretFile(t, dir, platform.SecretDBDSN, "svc:file@tcp(db:3306)/app")

	yaml := secretsBaseConfig +
		"db:\n  driver: mysql\n  dsn: \"svc:config@tcp(db:3306)/app\"\n" +
		"secrets:\n  providers: [file]\n  file:\n    dir: " + yamlDir(dir) + "\n"
	cfg, err := platform.LoadConfig(writeConfig(t, "secrets-miss.yaml", yaml))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.DB.DSN != "svc:file@tcp(db:3306)/app" {
		t.Fatalf("DB.DSN = %q, want file value", cfg.DB.DSN)
	}
}

// TestSecretsSplitsJWTRotationList 验证轮换期旧密钥列表按逗号切分并丢弃空白。
func TestSecretsSplitsJWTRotationList(t *testing.T) {
	dir := t.TempDir()
	writeSecretFile(t, dir, platform.SecretAuthJWTSecrets, "old-1, ,old-2,")

	yaml := secretsBaseConfig +
		"db:\n  driver: memory\n" +
		"secrets:\n  providers: [file]\n  file:\n    dir: " + yamlDir(dir) + "\n"
	cfg, err := platform.LoadConfig(writeConfig(t, "secrets-rot.yaml", yaml))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(cfg.Auth.JWT.Secrets) != 2 || cfg.Auth.JWT.Secrets[0] != "old-1" || cfg.Auth.JWT.Secrets[1] != "old-2" {
		t.Fatalf("auth.jwt.secrets = %v, want [old-1 old-2]", cfg.Auth.JWT.Secrets)
	}
}

// TestSecretsProdRejectsEnvOnly 验证生产禁止仅以环境变量作为密钥来源，
// 迁移期必须显式开启 allow_env_fallback 才放行。
//
// 只断言与 secrets 相关的错误：其它 prod 必填项不在本用例的验证范围内，
// 用「错误文本是否包含该规则的关键词」判定，避免为了凑齐 prod 基线
// 把用例写成与规则无关的字段堆砌。
func TestSecretsProdRejectsEnvOnly(t *testing.T) {
	prod := &platform.Config{Env: "prod"}
	prod.Secrets.Providers = []string{platform.SecretProviderEnv}
	if err := prod.Validate(); err == nil || !strings.Contains(err.Error(), "allow_env_fallback") {
		t.Fatalf("prod + providers:[env] 应被拒绝并提示 allow_env_fallback，err = %v", err)
	}

	migrating := &platform.Config{Env: "prod"}
	migrating.Secrets.Providers = []string{platform.SecretProviderEnv}
	migrating.Secrets.AllowEnvFallback = true
	if err := migrating.Validate(); err != nil && strings.Contains(err.Error(), "allow_env_fallback") {
		t.Fatalf("显式开启迁移豁免后不应再报该规则，err = %v", err)
	}
}

// TestSecretsVaultOverridesConfig 验证 vault 来源接入 normalize 阶段：
// 一个 KV 对象承载全部密钥，命中即覆盖配置文件中的明文。
func TestSecretsVaultOverridesConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/secret/data/admin-frame/prod" {
			t.Errorf("path = %q, want /v1/secret/data/admin-frame/prod", r.URL.Path)
		}
		if got := r.Header.Get("X-Vault-Token"); got != "s.inline" {
			t.Errorf("X-Vault-Token = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":{"data":{` +
			`"db_dsn":"svc:from-vault@tcp(db:3306)/app",` +
			`"auth_jwt_secret":"` + strings.Repeat("v", 32) + `"}}}`))
	}))
	defer server.Close()

	yaml := "service: admin-api\nchannel: admin\naddr: \":8081\"\n" +
		"auth:\n  jwt:\n    issuer: https://iam.example.com\n    audience: [admin]\n" +
		"db:\n  driver: mysql\n  dsn: \"svc:from-config@tcp(db:3306)/app\"\n" +
		"secrets:\n  providers: [vault]\n  vault:\n    address: \"" + server.URL + "\"\n" +
		"    token: \"s.inline\"\n    path: \"admin-frame/prod\"\n"
	cfg, err := platform.LoadConfig(writeConfig(t, "secrets-vault.yaml", yaml))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.DB.DSN != "svc:from-vault@tcp(db:3306)/app" {
		t.Fatalf("DB.DSN = %q, want vault value", cfg.DB.DSN)
	}
	if cfg.Auth.JWT.Secret != strings.Repeat("v", 32) {
		t.Fatalf("auth.jwt.secret 未被 vault 覆盖")
	}
}

// TestSecretsVaultRequiresAddress 验证地址缺失在启动期即阻断（构造期校验）。
func TestSecretsVaultRequiresAddress(t *testing.T) {
	yaml := secretsBaseConfig + "db:\n  driver: memory\n" +
		"secrets:\n  providers: [vault]\n  vault:\n    path: \"admin-frame/prod\"\n"
	_, err := platform.LoadConfig(writeConfig(t, "secrets-vault-noaddr.yaml", yaml))
	if err == nil {
		t.Fatalf("vault 缺地址应当被拒绝")
	}
	if !strings.Contains(err.Error(), "vault address is required") {
		t.Fatalf("错误应指向 vault address，实际 = %v", err)
	}
}

// TestSecretsVaultProdRejectsInlineToken 验证生产禁用显式令牌：
// 配置里出现令牌等于没有托管（SEC-05），必须改由 token_file / token_env 注入。
func TestSecretsVaultProdRejectsInlineToken(t *testing.T) {
	cfg := &platform.Config{Env: "prod"}
	cfg.Secrets.Providers = []string{platform.SecretProviderVault}
	cfg.Secrets.Vault.Address = "https://vault.internal:8200"
	cfg.Secrets.Vault.Path = "admin-frame/prod"
	cfg.Secrets.Vault.Token = "s.inline"
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "secrets.vault.token") {
		t.Fatalf("prod + 显式令牌应被拒绝，err = %v", err)
	}

	cfg.Secrets.Vault.Token = ""
	cfg.Secrets.Vault.TokenFile = "/var/run/secrets/vault/token"
	if err := cfg.Validate(); err != nil && strings.Contains(err.Error(), "secrets.vault.token") {
		t.Fatalf("改用 token_file 后不应再报令牌规则，err = %v", err)
	}
}

// TestSecretsFileRequiresDir 验证 file 来源必须给出目录。
func TestSecretsFileRequiresDir(t *testing.T) {
	yaml := secretsBaseConfig + "db:\n  driver: memory\n" + "secrets:\n  providers: [file]\n"
	_, err := platform.LoadConfig(writeConfig(t, "secrets-nodir.yaml", yaml))
	if err == nil {
		t.Fatalf("file 来源缺 dir 应当被拒绝")
	}
	if !strings.Contains(err.Error(), "secrets.file.dir") {
		t.Fatalf("错误应指向 secrets.file.dir，实际 = %v", err)
	}
}

// TestSecretsUnknownProvider 验证未知来源被拒绝。
func TestSecretsUnknownProvider(t *testing.T) {
	yaml := secretsBaseConfig + "db:\n  driver: memory\n" + "secrets:\n  providers: [bogus]\n"
	_, err := platform.LoadConfig(writeConfig(t, "secrets-unknown.yaml", yaml))
	if err == nil {
		t.Fatalf("未知 provider 应当被拒绝")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("错误应说明 unsupported，实际 = %v", err)
	}
}

// TestSecretsProviderNameDedup 验证来源名大小写归一与去重：
// `[FILE, file]` 不应因为重复而触发多条校验错误。
func TestSecretsProviderNameDedup(t *testing.T) {
	dir := t.TempDir()
	yaml := secretsBaseConfig +
		"db:\n  driver: memory\n" +
		"secrets:\n  providers: [FILE, file]\n  file:\n    dir: " + yamlDir(dir) + "\n"
	if _, err := platform.LoadConfig(writeConfig(t, "secrets-dedup.yaml", yaml)); err != nil {
		t.Fatalf("大小写重复的来源应当归一后通过，实际 error = %v", err)
	}
}

// TestLoadDBConfigUsesSecretProvider 验证迁移工具同样走托管源：
// 否则会出现「服务端读文件、迁移仍读环境变量」的分裂。
func TestLoadDBConfigUsesSecretProvider(t *testing.T) {
	dir := t.TempDir()
	writeSecretFile(t, dir, platform.SecretDBDSN, "migrate:from-file@tcp(db:3306)/app")

	yaml := "db:\n  driver: mysql\n  dsn: \"migrate:from-config@tcp(db:3306)/app\"\n" +
		"secrets:\n  providers: [file]\n  file:\n    dir: " + yamlDir(dir) + "\n"
	cfg, err := platform.LoadDBConfig(writeConfig(t, "secrets-migrate.yaml", yaml))
	if err != nil {
		t.Fatalf("LoadDBConfig() error = %v", err)
	}
	if cfg.DSN != "migrate:from-file@tcp(db:3306)/app" {
		t.Fatalf("DSN = %q, want file value", cfg.DSN)
	}
}

// TestSecretsErrorSurfacesThroughValidate 验证解析错误经 secretErr 汇总进启动错误，
// 且错误文本只含密钥名、不含取值（SEC-05）。
func TestSecretsErrorSurfacesThroughValidate(t *testing.T) {
	// 目录指向一个普通文件，读取时返回非 NotFound 的 IO 错误。
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	yaml := secretsBaseConfig +
		"db:\n  driver: mysql\n  dsn: \"a:b@tcp(db:3306)/app\"\n" +
		"secrets:\n  providers: [file]\n  file:\n    dir: \"" + blocker + "/sub\"\n"
	_, err := platform.LoadConfig(writeConfig(t, "secrets-ioerr.yaml", yaml))
	if err == nil {
		t.Fatalf("Provider 读取失败应当在启动期阻断")
	}
	if strings.Contains(err.Error(), "a:b@tcp") {
		t.Fatalf("错误文本不得回显密钥取值，实际 = %v", err)
	}
}

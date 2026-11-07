// Package e2e 是三端（admin / biz / c）端到端主链路测试（ENT-16）。
//
// 与 tests/integration 的差异：E2E 运行**真实二进制**（go build 后 exec），
// 覆盖 flags → 配置加载 → 资源装配 → 路由注册 → 中间件链的全链路，
// 是「可运行」而非「可编译」的验收。
//
// 依赖：E2E_MYSQL_DSN（MySQL 8，需 parseTime=true）。未设置时显式跳过——
// 该套件按规划为 nightly（C 级），不进 PR 必跑门禁，见 make check-e2e。
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/admin-frame/admin-frame/internal/testdb"
	"github.com/admin-frame/admin-frame/pkg/db"
)

// envDSN / envRedis 是 E2E 依赖的开关（也是 nightly 门禁的触发条件）。
//
// 身份域的撤销状态只支持 Redis（fail closed 语义），因此 Redis 是
// E2E 的硬依赖；逻辑库固定 db=15 与人工调试隔离。
const (
	envDSN   = "E2E_MYSQL_DSN"
	envRedis = "E2E_REDIS_ADDR"
	// envCallbackSecret 是供应商回调验签密钥的环境变量名（与
	// notificationruntime.EnvCallbackSecret 保持一致；跳包内私有常量
	// 防止 tests 引 cmd 内部实现）。
	envCallbackSecret = "ADMIN_FRAME_NOTIFICATION_CALLBACK_SECRET"
)

// bootstrapSubject / bootstrapPassword 与 identity/bootstrap 的公开演练口令一致
// （Admin#Bootstrap2026，首登强制改密）。
const (
	bootstrapSubject  = "admin@example.com"
	bootstrapPassword = "Admin#Bootstrap2026"
	newPassword       = "E2e#Passw0rd2026"
)

// TestThreeChannelMainFlow 三端主链路：
//
//	探针 → 未认证拒绝 → admin 登录（票据改密）→ 租户读写 → biz/c 通道隔离
//
// 单测试串行执行（E2E 关注链路而非并发；并发语义由集成矩阵覆盖）。
func TestThreeChannelMainFlow(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv(envDSN))
	redisAddr := strings.TrimSpace(os.Getenv(envRedis))
	if dsn == "" || redisAddr == "" {
		t.Skipf("%s / %s not set: e2e skipped (nightly job sets them; see make check-e2e)", envDSN, envRedis)
	}
	normalized, err := db.ForceMySQLCollation("mysql", dsn, db.MySQLDefaultCollation)
	if err != nil {
		t.Fatalf("normalize dsn: %v", err)
	}
	// 迁移 + 引导账号前置：二进制启动时会自行执行 EnsureAdmin，
	// 但系统角色（system_admin）必须已由迁移写入。
	migrateDatabase(t, normalized)

	// 供应商回调验签密钥（NT-07 fail closed：缺失时 c-api 拒绝装配回调端点）。
	// E2E 显式注入 ≥32 字节测试密钥，覆盖「密钥齐备 → 端点注册」合法路径。
	t.Setenv(envCallbackSecret, strings.Repeat("e2e-callback-secret-", 3))

	binDir := buildBinaries(t)
	admin := startService(t, filepath.Join(binDir, exeName("admin-api")), adminConfig(t, normalized, redisAddr))
	biz := startService(t, filepath.Join(binDir, exeName("biz-api")), bizConfig(t, normalized, redisAddr))
	c := startService(t, filepath.Join(binDir, exeName("c-api")), cConfig(t, normalized, redisAddr))

	// ① 三端探针：健康 / 版本 / 就绪（配置 → 资源 → 中间件链全部走通）。
	for _, svc := range []*service{admin, biz, c} {
		probeOK(t, svc)
	}

	// ② 未认证拒绝（401 + 40100）：中间件链在业务处理器之前生效。
	assertUnauthorized(t, admin.baseURL+"/admin/v1/tenants")
	assertUnauthorized(t, biz.baseURL+"/biz/v1/notifications")
	assertUnauthorized(t, c.baseURL+"/c/v1/me")

	// ③ admin 登录：引导账号首登强制改密（票据链路）→ 改密 → 再登录拿令牌。
	token := loginAdmin(t, admin.baseURL)

	// ④ 租户主链路：列表（权限快照生效）→ 创建 → 详情。
	status, body := doJSON(t, http.MethodGet, admin.baseURL+"/admin/v1/tenants", token, nil)
	if status != http.StatusOK || codeOf(body) != 0 {
		t.Fatalf("list tenants: status=%d body=%v", status, body)
	}

	code := fmt.Sprintf("e2e-%d", time.Now().UnixNano()%1_000_000)
	status, body = doJSON(t, http.MethodPost, admin.baseURL+"/admin/v1/tenants", token, map[string]any{
		"name": "E2E 租户 " + code,
		"code": code,
	})
	if status != http.StatusCreated || codeOf(body) != 0 {
		t.Fatalf("create tenant: status=%d body=%v", status, body)
	}
	tenantID := dataID(t, body)
	if tenantID == "" {
		t.Fatalf("create tenant returned empty id: %v", body)
	}

	status, body = doJSON(t, http.MethodGet, admin.baseURL+"/admin/v1/tenants/"+tenantID, token, nil)
	if status != http.StatusOK || codeOf(body) != 0 {
		t.Fatalf("get tenant: status=%d body=%v", status, body)
	}

	// ⑤ 通道隔离：admin 令牌打 B 端接口必须被拒（audience/channel 校验）。
	status, body = doJSON(t, http.MethodGet, biz.baseURL+"/biz/v1/notifications", token, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("admin token must be rejected on biz channel: status=%d body=%v", status, body)
	}
}

// ---- 服务编排 ----

// service 是一个已启动的进程。
type service struct {
	name    string
	baseURL string
	cmd     *exec.Cmd
	logBuf  *syncBuffer
}

// syncBuffer 是并发安全的进程日志缓冲（失败时输出便于排障）。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// buildBinaries 编译三端二进制（每进程一次，产物放入临时目录）。
func buildBinaries(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "admin-frame-e2e-bin-")
		if err != nil {
			binErr = err
			return
		}
		for _, name := range []string{"admin-api", "biz-api", "c-api"} {
			//nolint:gosec // G204: 参数来自测试内固定清单（三个已知服务名），非用户输入。
			cmd := exec.Command("go", "build", "-o", filepath.Join(dir, exeName(name)), "./cmd/"+name)
			cmd.Dir = repoRoot(t)
			out, err := cmd.CombinedOutput()
			if err != nil {
				binErr = fmt.Errorf("build %s: %v\n%s", name, err, out)
				return
			}
		}
		binPath = dir
	})
	if binErr != nil {
		t.Fatalf("build binaries: %v", binErr)
	}
	return binPath
}

// exeName 追加平台可执行后缀（Windows 上 go build -o 会产出 name.exe）。
func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// repoRoot 定位仓库根（测试工作目录是 tests/e2e）。
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

// migrateDatabase 应用全部迁移（二进制启动不跑迁移，由 cmd/migrate 或 Job 负责）。
func migrateDatabase(t *testing.T, dsn string) {
	t.Helper()
	database, err := db.Open(context.Background(), db.Config{Driver: "mysql", DSN: dsn})
	if err != nil {
		t.Fatalf("open e2e mysql: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := testdb.Migrate(context.Background(), database.SQL()); err != nil {
		t.Fatalf("migrate e2e database: %v", err)
	}
}

// startService 写临时配置并启动进程，等待健康检查通过。
func startService(t *testing.T, bin, configYAML string) *service {
	t.Helper()
	// 每次启动指向独立的本地端口（配置里 addr 已分配）。
	cfgPath := filepath.Join(t.TempDir(), filepath.Base(bin)+".yaml")
	if err := os.WriteFile(cfgPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	svc := &service{name: filepath.Base(bin), logBuf: &syncBuffer{}}
	//nolint:gosec // G204: bin 是构建产物路径，cfgPath 是测试临时配置，均非用户输入。
	svc.cmd = exec.Command(bin, "-config", cfgPath)
	svc.cmd.Dir = repoRoot(t)
	svc.cmd.Stdout = svc.logBuf
	svc.cmd.Stderr = svc.logBuf
	if err := svc.cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", svc.name, err)
	}
	t.Cleanup(func() {
		if svc.cmd.Process != nil {
			_ = svc.cmd.Process.Kill()
			_, _ = svc.cmd.Process.Wait()
		}
	})
	svc.baseURL = "http://" + addrFromConfig(configYAML)
	waitHealthy(t, svc)
	return svc
}

// waitHealthy 轮询 /healthz 直至 200 或超时（失败时附带进程日志）。
func waitHealthy(t *testing.T, svc *service) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(svc.baseURL + "/healthz")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		// 进程提前退出时立即失败，不等满超时。
		if svc.cmd.ProcessState != nil && svc.cmd.ProcessState.Exited() {
			t.Fatalf("%s exited during startup:\n%s", svc.name, svc.logBuf.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s not healthy within 60s:\n%s", svc.name, svc.logBuf.String())
}

// probeOK 断言健康 / 版本 / 就绪三个探针。
func probeOK(t *testing.T, svc *service) {
	t.Helper()
	for _, path := range []string{"/healthz", "/readyz", "/version"} {
		status, body := doJSON(t, http.MethodGet, svc.baseURL+path, "", nil)
		if status != http.StatusOK {
			t.Fatalf("%s %s: status=%d body=%v\nlogs:\n%s", svc.name, path, status, body, svc.logBuf.String())
		}
	}
}

// ---- 业务流 ----

// loginAdmin 走完「首登强制改密 → 改密 → 再登录」，返回访问令牌。
//
// 幂等：同一数据库重复运行时引导口令已被上一轮轮换，此时代码退化为
// 「用新口令直接登录」——E2E 不假设数据库是干净的一次性实例。
func loginAdmin(t *testing.T, baseURL string) string {
	t.Helper()
	status, body := doJSON(t, http.MethodPost, baseURL+"/admin/v1/auth/login", "", map[string]any{
		"subject":  bootstrapSubject,
		"password": bootstrapPassword,
	})
	if status == http.StatusUnauthorized {
		// 已轮换过口令（上一轮 E2E 改过密）：直接用新口令登录。
		status, body = doJSON(t, http.MethodPost, baseURL+"/admin/v1/auth/login", "", map[string]any{
			"subject":  bootstrapSubject,
			"password": newPassword,
		})
		if status != http.StatusOK {
			t.Fatalf("login with rotated password: status=%d body=%v", status, body)
		}
		data, _ := body["data"].(map[string]any)
		if token := tokenOf(data); token != "" {
			return token
		}
		t.Fatalf("token missing after rotated login: %v", body)
	}
	if status != http.StatusOK {
		t.Fatalf("bootstrap login: status=%d body=%v", status, body)
	}
	data, _ := body["data"].(map[string]any)
	if required, _ := data["password_change_required"].(bool); !required {
		// 已改过密（重复运行时）：直接用返回的令牌。
		if token := tokenOf(data); token != "" {
			return token
		}
		t.Fatalf("login without token and without change ticket: %v", body)
	}
	ticket, _ := data["password_change_ticket"].(string)
	if ticket == "" {
		t.Fatalf("password_change_ticket missing: %v", body)
	}

	status, body = doJSON(t, http.MethodPost, baseURL+"/admin/v1/auth/password/change", "", map[string]any{
		"new_password":           newPassword,
		"password_change_ticket": ticket,
	})
	if status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("change password: status=%d body=%v", status, body)
	}

	status, body = doJSON(t, http.MethodPost, baseURL+"/admin/v1/auth/login", "", map[string]any{
		"subject":  bootstrapSubject,
		"password": newPassword,
	})
	if status != http.StatusOK {
		t.Fatalf("login after change: status=%d body=%v", status, body)
	}
	data, _ = body["data"].(map[string]any)
	token := tokenOf(data)
	if token == "" {
		t.Fatalf("access token missing after login: %v", body)
	}
	return token
}

// tokenOf 从登录 data 中取访问令牌（兼容 access_token / token 两种字段）。
func tokenOf(data map[string]any) string {
	for _, key := range []string{"access_token", "token"} {
		if v, ok := data[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// assertUnauthorized 断言未认证请求被拒（401 + 业务码 40100）。
func assertUnauthorized(t *testing.T, url string) {
	t.Helper()
	status, body := doJSON(t, http.MethodGet, url, "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("%s must be 401 unauthenticated: status=%d body=%v", url, status, body)
	}
	if code := codeOf(body); code != 40100 {
		t.Fatalf("%s must carry code 40100: %v", url, body)
	}
}

// ---- HTTP 工具 ----

// doJSON 发请求并解析 JSON 响应（空体返回空 map）。
func doJSON(t *testing.T, method, url, token string, payload map[string]any) (status int, body map[string]any) {
	t.Helper()
	var bodyReader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body = map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	return resp.StatusCode, body
}

// codeOf 读取信封业务码。
func codeOf(body map[string]any) int {
	if v, ok := body["code"].(float64); ok {
		return int(v)
	}
	return -1
}

// dataID 读取信封 data.id。
func dataID(t *testing.T, body map[string]any) string {
	t.Helper()
	data, _ := body["data"].(map[string]any)
	if id, ok := data["id"].(string); ok {
		return id
	}
	return ""
}

// freePort 申请一个空闲本地端口（绑定后立即释放，供配置使用）。
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate port: %v", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

// addrFromConfig 从配置文本读取 addr（供 baseURL 使用）。
func addrFromConfig(configYAML string) string {
	for _, line := range strings.Split(configYAML, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "addr:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "addr:")), `"`)
		}
	}
	return ""
}

// ---- 配置生成（内存 Redis/Storage，MySQL 持久化） ----

// e2eJWTSecret 返回测试用 JWT 密钥。
//
// 由固定片段拼出而不是写字面量：SEC-01 的 gitleaks 会按熵值把长随机串判为泄漏，
// 测试夹具也不应训练团队「随手粘贴密钥」的习惯（见 .gitleaks.toml 的同名说明）。
func e2eJWTSecret() string {
	return strings.Repeat("e2e-jwt-", 8)
}

// redisBlock 生成 Redis 配置（身份域撤销状态与限流共用；db=15 隔离）。
func redisBlock(addr string) string {
	return fmt.Sprintf(`redis:
  addr: "%s"
  db: 15
`, addr)
}

// identityBlock 是三端共用的身份域配置。
//
// 密钥必须与 internal/testdb.Migrate 种入引导管理员时使用的测试密钥一致
// （hash = 0x11×32，subject key ring = 0x22×32 / k1）——否则 subject_index
// 不匹配，二进制启动会重复插入引导账号并撞主键。
func identityBlock(extra string) string {
	return fmt.Sprintf(`identity:
  enabled: true
  hash_key: "%s"
  revocation:
    driver: redis
  mfa:
    admin_required: false
  subject:
    active_key_id: k1
    keys:
      - id: k1
        key: "%s"
%s`, strings.Repeat("11", 32), strings.Repeat("22", 32), extra)
}

// adminConfig 生成 A 端配置：MySQL + IAM enforce + 身份域（引导账号种子的宿主）。
func adminConfig(t *testing.T, dsn, redisAddr string) string {
	t.Helper()
	return fmt.Sprintf(`env: test
service: admin-api
channel: admin
addr: "127.0.0.1:%d"
shutdown_timeout: 3s
log:
  level: error
  format: json
async:
  enabled: true
db:
  driver: mysql
  dsn: "%s"
%s
auth:
  mode: jwt
  expected_channel: admin
  jwt:
    secret: "%s"
    issuer: e2e
    audience: [admin]
id:
  node_id: 11
iam:
  mode: enforce
  cache:
    driver: memory
  approval:
    enabled: false
%s
`, freePort(t), dsn, redisBlock(redisAddr), e2eJWTSecret(), identityBlock("  sms:\n    enabled: false\n"))
}

// bizConfig 生成 B 端配置：IAM enforce（商户级角色），身份域开启（B 端登录）。
func bizConfig(t *testing.T, dsn, redisAddr string) string {
	t.Helper()
	return fmt.Sprintf(`env: test
service: biz-api
channel: biz
addr: "127.0.0.1:%d"
shutdown_timeout: 3s
log:
  level: error
  format: json
async:
  enabled: true
db:
  driver: mysql
  dsn: "%s"
%s
auth:
  mode: jwt
  expected_channel: biz
  jwt:
    secret: "%s"
    issuer: e2e
    audience: [biz]
id:
  node_id: 12
iam:
  mode: enforce
  cache:
    driver: memory
  approval:
    enabled: false
%s
`, freePort(t), dsn, redisBlock(redisAddr), e2eJWTSecret(), identityBlock("  sms:\n    enabled: false\n"))
}

// cConfig 生成 C 端配置：IAM off（C 端不挂载）、身份域开启（短信登录入口）。
func cConfig(t *testing.T, dsn, redisAddr string) string {
	t.Helper()
	return fmt.Sprintf(`env: test
service: c-api
channel: c
addr: "127.0.0.1:%d"
shutdown_timeout: 3s
log:
  level: error
  format: json
async:
  enabled: true
db:
  driver: mysql
  dsn: "%s"
%s
auth:
  mode: jwt
  expected_channel: c
  jwt:
    secret: "%s"
    issuer: e2e
    audience: [c]
id:
  node_id: 13
iam:
  mode: off
  cache:
    driver: memory
  approval:
    enabled: false
%s
`, freePort(t), dsn, redisBlock(redisAddr), e2eJWTSecret(), identityBlock("  sms:\n    enabled: true\n    driver: memory\n    template_id: SMS_E2E\n    sign_name: E2E\n    locale: zh-CN\n"))
}

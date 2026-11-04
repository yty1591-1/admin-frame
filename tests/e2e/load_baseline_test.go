// 全链路压测基线执行器（ENT-16 收尾：压测基线登记）。
//
// 与 k6 脚本（tests/load/identity_login.js）的分工：
//   - k6 脚本面向 staging，验证 docs/identity-slo.md 的登录 / 刷新 / MFA 时延 SLO；
//   - 本执行器面向「同一台机器可复现」：拉真实二进制 + MySQL + Redis，
//     压**已认证读路径**（鉴权中间件 → 权限快照 → 仓储 SQL → 序列化），
//     产出与本机硬件绑定的分位值，登记在 docs/perf/LOAD_BASELINES.md。
//
// 为什么不压登录：bcrypt 成本使结论随 CPU 核数剧烈波动（同等 QPS 下
// 机器差一倍，p99 差数倍），不适合作为「同机可比」的基线；登录 SLO 由
// staging 的 k6 场景负责。
//
// 默认跳过（PERF-06：压测不进 PR 必跑门禁）。手动执行：
//
//	E2E_LOAD_BASELINE=1 E2E_MYSQL_DSN=... E2E_REDIS_ADDR=... \
//	  go test ./tests/e2e/ -run TestLoadBaselineAdminRead -count=1 -v
package e2e_test

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/db"
)

// 压测参数环境变量。
const (
	envLoadBaseline = "E2E_LOAD_BASELINE" // =1 启用
	envLoadRate     = "E2E_LOAD_RATE"     // 目标 QPS，默认 50
	envLoadDuration = "E2E_LOAD_DURATION" // 采样时长，默认 30s
	envLoadWarmup   = "E2E_LOAD_WARMUP"   // 预热时长，默认 5s（不计入样本）
	envLoadArtifact = "E2E_LOAD_ARTIFACT" // 产物路径（JSON），缺省只写日志
	envLoadStrict   = "E2E_LOAD_STRICT"   // =1 时本机时延不达标即失败
)

// 读路径基线目标（与 docs/perf/LOAD_BASELINES.md 的「目标」列一一对应）。
// 取值依据：API_STANDARDS 的同步读接口预算 + 本机实测留 3 倍余量，
// 仅用于判读，不作为默认门禁（PERF-06）。
const (
	loadTargetP95 = 100 * time.Millisecond
	loadTargetP99 = 300 * time.Millisecond
)

// loadSample 是一次请求的观测结果（status=0 表示传输层失败）。
type loadSample struct {
	status  int
	code    int
	latency time.Duration
}

// loadReport 是登记表需要的最小事实集（写入 artifact 与测试日志）。
type loadReport struct {
	Scenario    string  `json:"scenario"`
	Target      string  `json:"target"`
	Rate        int     `json:"rate_rps"`
	Duration    string  `json:"duration"`
	Warmup      string  `json:"warmup"`
	Samples     int     `json:"samples"`
	SuccessRate float64 `json:"success_rate"`
	Errors      int     `json:"errors"` // 传输失败 + 5xx（4xx 单列在 success_rate 中体现）
	P50ms       float64 `json:"p50_ms"`
	P95ms       float64 `json:"p95_ms"`
	P99ms       float64 `json:"p99_ms"`
	MaxMs       float64 `json:"max_ms"`
	TargetP95ms float64 `json:"target_p95_ms"`
	TargetP99ms float64 `json:"target_p99_ms"`
	Passed      bool    `json:"passed"`
	GoVersion   string  `json:"go_version"`
	CPUs        int     `json:"cpus"`
	StartedAt   string  `json:"started_at"`
}

// TestLoadBaselineAdminRead 压 A 端「租户列表」读路径。
//
// 该路径覆盖链路的绝大部分成本：JWT 校验 → IAM 权限快照（内存缓存）→
// 仓储 SQL → 信封序列化，且不受 bcrypt 与验证码等非线性因素干扰，
// 适合作为「同一台机器前后可比」的回归基线。
func TestLoadBaselineAdminRead(t *testing.T) {
	if os.Getenv(envLoadBaseline) != "1" {
		t.Skipf("%s != 1: load baseline skipped (manual/nightly only, see make load-baseline)", envLoadBaseline)
	}
	dsn := strings.TrimSpace(os.Getenv(envDSN))
	redisAddr := strings.TrimSpace(os.Getenv(envRedis))
	if dsn == "" || redisAddr == "" {
		t.Skipf("%s / %s not set: load baseline skipped (see make load-baseline)", envDSN, envRedis)
	}
	rate := envInt(t, envLoadRate, 50)
	duration := envDuration(t, envLoadDuration, 30*time.Second)
	warmup := envDuration(t, envLoadWarmup, 5*time.Second)
	if rate <= 0 || duration <= 0 {
		t.Fatalf("invalid load params: rate=%d duration=%s", rate, duration)
	}

	normalized, err := db.ForceMySQLCollation("mysql", dsn, db.MySQLDefaultCollation)
	if err != nil {
		t.Fatalf("normalize dsn: %v", err)
	}
	migrateDatabase(t, normalized)
	binDir := buildBinaries(t)
	admin := startService(t, filepath.Join(binDir, exeName("admin-api")), adminConfig(t, normalized, redisAddr))

	// 令牌只取一次：压的是读路径，不在压测循环里重复登录。
	token := loginAdmin(t, admin.baseURL)
	url := admin.baseURL + "/admin/v1/tenants?page_size=20"

	client := loadClient(rate)
	defer client.CloseIdleConnections()

	// 预热：填充连接池与权限快照缓存，样本丢弃（避免把冷启动算成分位）。
	runLoadAt(t, client, url, token, rate, warmup)
	samples, startedAt := runLoadAt(t, client, url, token, rate, duration)
	if len(samples) == 0 {
		t.Fatalf("no samples collected (rate=%d duration=%s)", rate, duration)
	}

	report := buildReport(samples, rate, duration, warmup, startedAt)
	report.GoVersion = runtime.Version()
	report.CPUs = runtime.NumCPU()

	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	t.Logf("load baseline: %s", string(raw))
	if path := strings.TrimSpace(os.Getenv(envLoadArtifact)); path != "" {
		writeArtifact(t, path, raw)
	}

	// 硬断言只留「链路正确性」：无传输失败 / 5xx，成功率 ≥99%。
	// 时延目标默认只判读（本机噪声大），E2E_LOAD_STRICT=1 时升级为失败。
	if report.Errors != 0 {
		t.Fatalf("baseline saw %d transport/5xx errors: %s", report.Errors, string(raw))
	}
	if report.SuccessRate < 0.99 {
		t.Fatalf("success rate %.4f < 0.99: %s", report.SuccessRate, string(raw))
	}
	if os.Getenv(envLoadStrict) == "1" && !report.Passed {
		t.Fatalf("latency target missed (p95=%0.2fms p99=%0.2fms): %s", report.P95ms, report.P99ms, string(raw))
	}
}

// writeArtifact 落盘报告（目录不存在时创建）。
func writeArtifact(t *testing.T, path string, raw []byte) {
	t.Helper()
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		//nolint:gosec // G703: 产物路径来自显式环境变量（执行者指定），不来自请求输入。
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("create artifact dir: %v", err)
		}
	}
	//nolint:gosec // G703: 同上，路径由 E2E_LOAD_ARTIFACT 显式指定。
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	t.Logf("artifact written: %s", path)
}

// loadClient 构造压测用客户端：连接池按目标并发放大，避免压到本机端口耗尽。
func loadClient(rate int) *http.Client {
	pool := rate * 2
	if pool < 32 {
		pool = 32
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:               nil, // 不走环境代理：压测目标是本机进程
			DialContext:         (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:        pool,
			MaxIdleConnsPerHost: pool,
			MaxConnsPerHost:     pool,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true,
		},
	}
}

// runLoadAt 以「固定到达率」发压，返回全部样本与起始时刻。
//
// 用令牌间隔而不是闭路循环，保证到达率可复现：闭路压测的实际 QPS 会被
// 服务端时延反向钳制，无法与登记表的目标 QPS 对齐。
func runLoadAt(t *testing.T, client *http.Client, url, token string, rate int, duration time.Duration) ([]loadSample, time.Time) {
	t.Helper()
	interval := time.Second / time.Duration(rate)
	if interval <= 0 {
		interval = time.Nanosecond
	}
	// 缓冲 1 秒的量：消费端短暂抖动时不丢 tick，持续饱和则丢弃（保到达率、不积压）。
	jobs := make(chan struct{}, rate)
	results := make(chan loadSample, rate*2)

	// 并发上限取目标 QPS 的 1/4（最少 2）：按 Little 定律，p99 在 1s 内时
	// 该并发足以吃满目标速率；不足时表现为丢 tick，而非把时延算进队列。
	workers := rate / 4
	if workers < 2 {
		workers = 2
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				results <- doLoadRequest(client, url, token)
			}
		}()
	}

	startedAt := time.Now()
	deadline := startedAt.Add(duration)
	go func() {
		defer close(jobs)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for now := time.Now(); now.Before(deadline); now = <-ticker.C {
			select {
			case jobs <- struct{}{}:
			default:
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	collected := make([]loadSample, 0, rate)
	for sample := range results {
		collected = append(collected, sample)
	}
	return collected, startedAt
}

// doLoadRequest 发一次读请求并记录时延与信封码。
func doLoadRequest(client *http.Client, url, token string) loadSample {
	req, err := http.NewRequest(http.MethodGet, url, http.NoBody)
	if err != nil {
		return loadSample{status: 0, code: -1}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return loadSample{status: 0, code: -1, latency: latency}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	code := -1
	var envelope struct {
		Code int `json:"code"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &envelope) == nil {
		code = envelope.Code
	}
	return loadSample{status: resp.StatusCode, code: code, latency: latency}
}

// buildReport 由样本汇总出登记用的分位与判定。
func buildReport(samples []loadSample, rate int, duration, warmup time.Duration, startedAt time.Time) loadReport {
	latencies := make([]time.Duration, 0, len(samples))
	success, failures := 0, 0
	for _, s := range samples {
		latencies = append(latencies, s.latency)
		if s.status == http.StatusOK && s.code == 0 {
			success++
		}
		if s.status == 0 || s.status >= 500 {
			failures++
		}
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	report := loadReport{
		Scenario:    "admin.tenants.list",
		Target:      "GET /admin/v1/tenants?page_size=20（鉴权 + 权限快照 + SQL + 序列化）",
		Rate:        rate,
		Duration:    duration.String(),
		Warmup:      warmup.String(),
		Samples:     len(samples),
		SuccessRate: float64(success) / float64(len(samples)),
		Errors:      failures,
		P50ms:       ms(percentile(latencies, 50)),
		P95ms:       ms(percentile(latencies, 95)),
		P99ms:       ms(percentile(latencies, 99)),
		MaxMs:       ms(latencies[len(latencies)-1]),
		TargetP95ms: ms(loadTargetP95),
		TargetP99ms: ms(loadTargetP99),
		StartedAt:   startedAt.UTC().Format(time.RFC3339),
	}
	report.Passed = report.P95ms <= report.TargetP95ms && report.P99ms <= report.TargetP99ms && failures == 0
	return report
}

// percentile 取最近秩分位（不插值：样本足够时差异可忽略，且可复现）。
func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p*len(sorted) + 99) / 100 // ceil(p*n/100)
	if idx < 1 {
		idx = 1
	}
	if idx > len(sorted) {
		idx = len(sorted)
	}
	return sorted[idx-1]
}

// ms 转毫秒（保留两位小数，报告可读）。
func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// envInt 读整型环境变量（缺省回退）。
func envInt(t *testing.T, key string, fallback int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s must be an integer: %q", key, raw)
	}
	return v
}

// envDuration 读时长环境变量（支持 30s / 2m 等 Go duration 字面量）。
func envDuration(t *testing.T, key string, fallback time.Duration) time.Duration {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		t.Fatalf("%s must be a duration: %q", key, raw)
	}
	return v
}

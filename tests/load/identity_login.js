// 身份域压测脚本（MNT-04 / R-02）：密码登录、刷新、MFA 两步校验。
//
// 用途：在 staging（生产同规格 DB/Redis）验证 docs/identity-slo.md §1 的时延 SLO。
//
// 运行前准备（staging 执行清单）：
//   1. 预置压测账号：LOGIN_SUBJECTS 中的主体均已激活，且 PASSWORD 为其当前密码；
//      若跑 MFA 场景，账号已完成 TOTP 绑定，TOTP_SECRET 为其 Base32 密钥。
//   2. 关闭或放宽登录限流（仅压测环境），避免 429 混入样本：
//      identity.login_rate_limit.rps/burst 与端点限流按目标 QPS 调整。
//   3. 采样 10 分钟/档，记录 k6 摘要 + Prometheus 的 identity_* 直方图。
//   4. 每档结束检查：5xx=0、`identity_token_reuse_total` 仅在重放用例增长、
//      DB 连接池无等待、Redis RTT 正常（见 docs/identity-slo.md §3）。
//   5. 通过标准：login p95<=300ms / p99<=800ms；refresh p95<=500ms；mfa p95<=500ms；
//      业务失败仅来自预期的错误密码 / 不存在账号 / 重放用例。
//
// 运行示例：
//   k6 run tests/load/identity_login.js -e BASE_URL=https://staging.example.com \
//     -e CHANNEL=admin -e LOGIN_SUBJECTS=load-01,load-02 -e PASSWORD='Load#Passw0rd'
//   SCENARIO=refresh k6 run ...（可选 login|refresh|mfa，默认 login）
//
// 环境变量：
//   BASE_URL（必填）、CHANNEL（admin|biz，默认 admin）、PASSWORD（必填）
//   LOGIN_SUBJECTS（逗号分隔；未给 ACCOUNTS_FILE 时必填）
//   ACCOUNTS_FILE（可选）：账号池文件，每行 `subject[,totp_secret]`，用于 A 端
//     mfa.admin_required=true 时「一账号一 TOTP」的真实分布；给出时覆盖 LOGIN_SUBJECTS
//   TOTP_SECRET（mfa / refresh 场景在单密钥模式下必填）
//   WRONG_PASSWORD（默认在正确密码后追加 #）、MERCHANT_ID（B 端可选）
//   RATE（每秒到达数，默认 50）、DURATION（默认 10m）
import crypto from 'k6/crypto';
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import execution from 'k6/execution';

const requestedScenario = (__ENV.SCENARIO || 'login').toLowerCase();
// 仅支持单场景运行，避免空指标导致阈值误判。
const scenario = requestedScenario === 'refresh' || requestedScenario === 'mfa' ? requestedScenario : 'login';
const baseURL = (__ENV.BASE_URL || '').replace(/\/+$/, '');
const channel = (__ENV.CHANNEL || 'admin').toLowerCase();
// 压测账号池：ACCOUNTS_FILE 优先（每行 subject[,totp_secret]），用于生产规格
// A 端（mfa.admin_required=true）下「一账号一 TOTP」的真实密钥分布；
// 未提供时退回 LOGIN_SUBJECTS 单密钥模式，保持既有用法可用。
const accountsFile = __ENV.ACCOUNTS_FILE || '';
const fileAccounts = accountsFile
  ? open(accountsFile)
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line && !line.startsWith('#'))
    .map((line) => {
      const [subject, totp] = line.split(',');
      return { subject: (subject || '').trim(), totp: (totp || '').trim() };
    })
    .filter((item) => item.subject)
  : [];
const subjects = fileAccounts.length
  ? fileAccounts.map((item) => item.subject)
  : (__ENV.LOGIN_SUBJECTS || '').split(',').map((s) => s.trim()).filter(Boolean);
const password = __ENV.PASSWORD || '';
const wrongPassword = __ENV.WRONG_PASSWORD || `${password}#`;
const merchantID = __ENV.MERCHANT_ID || '';
const totpSecret = __ENV.TOTP_SECRET || '';
const totpBySubject = new Map(fileAccounts.filter((item) => item.totp).map((item) => [item.subject, item.totp]));
const rate = parseInt(__ENV.RATE || '50', 10);
const duration = __ENV.DURATION || '10m';

// totpFor 取主体对应的 TOTP 密钥：账号池文件优先，单密钥模式回退 TOTP_SECRET。
function totpFor(subject) {
  return totpBySubject.get(subject) || totpSecret;
}

const loginDuration = new Trend('identity_login_duration', true);
const refreshDuration = new Trend('identity_refresh_duration', true);
const mfaDuration = new Trend('identity_mfa_duration', true);
const outcomes = new Counter('identity_flow_outcome');
// 登记表口径：errors 只统计「意外结果」，未测到目标操作的迭代单列 skipped。
const unexpected = new Counter('identity_flow_unexpected');
const skipped = new Counter('identity_flow_skipped');
// measured 与目标操作的 Trend 同步自增：k6 摘要里 Trend 不暴露 count，
// 用计数器给出登记表的 samples 口径。
const measured = new Counter('identity_flow_measured');
const skipCases = new Set(['refresh_login_failed', 'mfa_challenge_missing', 'mfa_replay_rejected']);
// 预期拒绝：错误口令 / 不存在账号 / 错误动态码 / 已轮换令牌重放均返回 40100。
// 不排除时 http_req_failed 会把这 30% 的用例流量算成失败，阈值恒不可达。
const expectedAuth = http.expectedStatuses(200, 401);

// thresholds 只对启用的场景生效，避免空指标影响判定。
const thresholds = { http_req_failed: ['rate<0.02'] };
if (scenario === 'login') {
  thresholds['identity_login_duration'] = ['p(95)<300', 'p(99)<800'];
}
if (scenario === 'refresh') {
  thresholds['identity_refresh_duration'] = ['p(95)<500'];
}
if (scenario === 'mfa') {
  thresholds['identity_mfa_duration'] = ['p(95)<500'];
}

const exec = { login: 'loginFlow', refresh: 'refreshFlow', mfa: 'mfaFlow' }[scenario];

export const options = {
  scenarios: {
    [scenario]: {
      executor: 'ramping-arrival-rate',
      startRate: Math.max(1, Math.floor(rate / 10)),
      timeUnit: '1s',
      preAllocatedVUs: Math.max(10, rate * 2),
      maxVUs: Math.max(20, rate * 4),
      stages: [
        { duration: '1m', target: rate },
        { duration, target: rate },
        { duration: '1m', target: 0 },
      ],
      exec,
    },
  },
  thresholds,
  // p(99) 默认不进摘要，登记表需要 p99 必须显式声明。
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
};

function requiredEnv() {
  const missing = [];
  if (!baseURL) missing.push('BASE_URL');
  if (!subjects.length) missing.push(accountsFile ? 'ACCOUNTS_FILE(no entries)' : 'LOGIN_SUBJECTS');
  if (!password) missing.push('PASSWORD');
  // refresh 场景在生产规格下（mfa.admin_required=true）也要过 MFA 才能拿到刷新令牌。
  if ((scenario === 'mfa' || scenario === 'refresh') && !totpSecret && totpBySubject.size === 0) {
    missing.push('TOTP_SECRET');
  }
  return missing;
}

export function setup() {
  const missing = requiredEnv();
  if (missing.length) {
    throw new Error(`missing required env: ${missing.join(', ')}`);
  }
  return { startedAt: Date.now() };
}

function loginBody(subject, credential) {
  const body = { subject, password: credential };
  if (channel === 'biz' && merchantID) body.merchant_id = merchantID;
  return JSON.stringify(body);
}

function login(subject, credential) {
  return http.post(`${baseURL}/${channel}/v1/auth/login`, loginBody(subject, credential), {
    headers: { 'Content-Type': 'application/json' },
    tags: { endpoint: 'login' },
    responseCallback: expectedAuth,
  });
}

function envelope(res) {
  try {
    return JSON.parse(res.body);
  } catch (err) {
    return {};
  }
}

function recordOutcome(name) {
  outcomes.add(1, { case: name });
  if (name.endsWith('_unexpected')) {
    unexpected.add(1);
  } else if (skipCases.has(name)) {
    skipped.add(1);
  }
}

// loginFlow 复现 SLO 文档的用户分布：正确密码 70%、错误密码 20%、不存在账号 10%。
export function loginFlow() {
  const roll = Math.random();
  const subject = subjects[Math.floor(Math.random() * subjects.length)];
  const credential = roll < 0.7 ? password : wrongPassword;
  const target = roll < 0.9 ? subject : `${subject}-missing`;

  const res = login(target, credential);
  loginDuration.add(res.timings.duration);
  measured.add(1);

  if (roll < 0.7) {
    const env = envelope(res);
    const ok = check(res, {
      'login accepted': (r) => r.status === 200 && (env.code === 0),
      'login returns token or intermediate state': () => !!(env.data && (env.data.access_token || env.data.mfa_required || env.data.mfa_binding_required || env.data.password_change_required)),
    });
    recordOutcome(ok ? 'login_success' : 'login_unexpected');
    return;
  }
  const rejected = check(res, {
    'bad credentials rejected': (r) => r.status === 401 && envelope(r).code === 40100,
  });
  recordOutcome(rejected ? 'login_expected_reject' : 'login_unexpected');
}

// refreshFlow 覆盖正常刷新（90%）与已轮换令牌重放（10%）。
//
// 生产规格 A 端的刷新来自「已有会话的客户端」：登录 + MFA 已经发生过，refresh 只做轮换。
// 因此本场景为每个 VU 固定一个账号并在本地维持轮换中的令牌；若每次 refresh 都重新
// 登录 + 校验 MFA，会把 3 倍请求量和 Argon2 成本算进刷新时延，得到的是登录链路的排队
// 时间而不是 refresh 的 SLO（STG-EQ-01 首次执行即踩到此坑：p95 1.3s vs 目标 500ms）。
//
// 重放用例按设计会整链撤销，之后必须重新登录，因此每 10 次迭代约有 1 次登录开销。
const sessions = new Map(); // subject -> 当前有效 refresh_token（进程内，按 VU 分片）

function verifyMFA(subject, data) {
  const verify = http.post(`${baseURL}/${channel}/v1/auth/mfa/verify`, JSON.stringify({
    mfa_ticket: data.mfa_ticket,
    code: totpCode(totpFor(subject)),
  }), {
    headers: { 'Content-Type': 'application/json' },
    tags: { endpoint: 'mfa_verify' },
    responseCallback: expectedAuth,
  });
  return envelope(verify).data || {};
}

// sessionToken 取该账号当前可用的刷新令牌；没有则登录（含 MFA 前置）后建立会话。
function sessionToken(subject) {
  const cached = sessions.get(subject);
  if (cached) {
    return cached;
  }
  let data = envelope(login(subject, password)).data || {};
  if (data.mfa_required && data.mfa_ticket) {
    data = verifyMFA(subject, data);
  }
  if (!data.refresh_token) {
    return '';
  }
  sessions.set(subject, data.refresh_token);
  return data.refresh_token;
}

export function refreshFlow() {
  // 按 VU 分片固定账号：不同 VU 不共用同一 TOTP 时间步，避免被服务端按重放拒绝。
  const subject = subjects[(execution.vu.idInTest - 1) % subjects.length];
  const token = sessionToken(subject);
  if (!token) {
    recordOutcome('refresh_login_failed');
    return;
  }
  const refresh = (value) => http.post(`${baseURL}/${channel}/v1/auth/refresh`, JSON.stringify({ refresh_token: value }), {
    headers: { 'Content-Type': 'application/json' },
    tags: { endpoint: 'refresh' },
    responseCallback: expectedAuth,
  });

  const res = refresh(token);
  refreshDuration.add(res.timings.duration);
  measured.add(1);
  const data = envelope(res).data || {};
  if (res.status === 200 && data.refresh_token) {
    sessions.set(subject, data.refresh_token);
  } else {
    sessions.delete(subject);
  }
  const ok = check(res, {
    'refresh accepted': (r) => r.status === 200 && envelope(r).code === 0,
  });
  recordOutcome(ok ? 'refresh_success' : 'refresh_unexpected');

  if (Math.random() < 0.1) {
    // 旧令牌已被轮换：必须整链撤销并返回 40100（会话随之失效，下轮重新登录）。
    const replay = refresh(token);
    refreshDuration.add(replay.timings.duration);
    measured.add(1);
    sessions.delete(subject);
    const rejected = check(replay, {
      'rotated token rejected': (r) => r.status === 401 && envelope(r).code === 40100,
    });
    recordOutcome(rejected ? 'refresh_replay_rejected' : 'refresh_replay_unexpected');
  }
}

// mfaFlow 覆盖 TOTP 成功（50%）与错误码（50%）。
export function mfaFlow() {
  const subject = subjects[Math.floor(Math.random() * subjects.length)];
  const loginRes = login(subject, password);
  const data = envelope(loginRes).data || {};
  if (!data.mfa_ticket) {
    recordOutcome('mfa_challenge_missing');
    return;
  }
  const expectSuccess = Math.random() < 0.5;
  const code = expectSuccess ? totpCode(totpFor(subject)) : '000000';
  const res = http.post(`${baseURL}/${channel}/v1/auth/mfa/verify`, JSON.stringify({ mfa_ticket: data.mfa_ticket, code }), {
    headers: { 'Content-Type': 'application/json' },
    tags: { endpoint: 'mfa_verify' },
    responseCallback: expectedAuth,
  });
  mfaDuration.add(res.timings.duration);
  measured.add(1);
  if (expectSuccess) {
    const ok = check(res, {
      'mfa accepted': (r) => r.status === 200 && envelope(r).code === 0 && !!envelope(r).data.access_token,
    });
    if (ok) {
      recordOutcome('mfa_success');
    } else if (res.status === 401 && envelope(res).code === 40100) {
      // 同一账号在同一 30s 时间步内被并发校验时，服务端按 TOTP 重放拒绝（last_used_step
      // 单调递增，属正确行为）。账号池按 50 rps 复用时命中率约 1%，单独归类，
      // 不计入「意外失败」，但仍保留 40100 错误码口径。
      recordOutcome('mfa_replay_rejected');
    } else {
      recordOutcome('mfa_unexpected');
    }
  } else {
    const rejected = check(res, {
      'wrong totp rejected': (r) => r.status === 401 && envelope(r).code === 40100,
    });
    recordOutcome(rejected ? 'mfa_expected_reject' : 'mfa_unexpected');
  }
  sleep(0.05);
}

// ---- TOTP（RFC 6238，SHA-1 / 6 位 / 30s）：压测账号已绑定时的动态码 ----

const base32Alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';

function base32Decode(input) {
  const clean = input.replace(/=+$/, '').toUpperCase();
  let bits = 0;
  let value = 0;
  const out = [];
  for (let i = 0; i < clean.length; i += 1) {
    const idx = base32Alphabet.indexOf(clean[i]);
    if (idx < 0) throw new Error(`invalid base32 character: ${clean[i]}`);
    value = ((value << 5) | idx) & 0xffff;
    bits += 5;
    if (bits >= 8) {
      bits -= 8;
      out.push((value >>> bits) & 0xff);
    }
  }
  return out;
}

function hexToBytes(hex) {
  const out = [];
  for (let i = 0; i < hex.length; i += 2) {
    out.push(parseInt(hex.substr(i, 2), 16));
  }
  return out;
}

// bytesToArrayBuffer：k6 的 crypto.hmac 只接受 string / ArrayBuffer，
// 直接传 JS 数组会抛 invalid type。
function bytesToArrayBuffer(bytes) {
  const buf = new ArrayBuffer(bytes.length);
  const view = new Uint8Array(buf);
  for (let i = 0; i < bytes.length; i += 1) view[i] = bytes[i];
  return buf;
}

function totpCode(secret, digits = 6, period = 30) {
  const key = bytesToArrayBuffer(base32Decode(secret));
  const counter = Math.floor(Date.now() / 1000 / period);
  const buf = new ArrayBuffer(8);
  const view = new DataView(buf);
  view.setUint32(0, Math.floor(counter / 0x100000000));
  view.setUint32(4, counter % 0x100000000);
  const bytes = hexToBytes(crypto.hmac('sha1', key, buf, 'hex'));
  const offset = bytes[bytes.length - 1] & 0x0f;
  const binary = ((bytes[offset] & 0x7f) << 24) | (bytes[offset + 1] << 16) | (bytes[offset + 2] << 8) | bytes[offset + 3];
  return String(binary % 10 ** digits).padStart(digits, '0');
}

// handleSummary 产出登记表（docs/perf/LOAD_BASELINES.md）要求的产物结构。
// 字段与 tests/load/artifacts/*.json、check-load-baseline 的 LB-03 断言对齐：
// samples / errors / success_rate / p95_ms 缺一即门禁失败。
// 输出路径用 -e ARTIFACT=/scripts/artifacts/<name>.json 指定；未指定时只打印摘要。
export function handleSummary(data) {
  const round3 = (value) => (typeof value === 'number' ? Math.round(value * 1000) / 1000 : value);
  const metrics = data.metrics || {};
  const valuesOf = (name) => (metrics[name] ? metrics[name].values : undefined) || {};
  const countOf = (name) => valuesOf(name).count || 0;

  const durationMetric = {
    login: 'identity_login_duration',
    refresh: 'identity_refresh_duration',
    mfa: 'identity_mfa_duration',
  }[scenario];
  const trend = valuesOf(durationMetric);
  const failedRate = metrics.http_req_failed ? metrics.http_req_failed.values.rate : 0;
  const elapsed = (data.state && data.state.testRunDurationMs ? data.state.testRunDurationMs : 0) / 1000;

  // samples = 目标操作的测点数（refresh 每迭代 1~2 次，重放用例也算一次被测刷新）。
  const samples = countOf('identity_flow_measured');
  const errors = countOf('identity_flow_unexpected');
  const skippedCount = countOf('identity_flow_skipped');

  const artifact = {
    scenario: `identity.${scenario}`,
    target: {
      login: 'POST /admin/v1/auth/login（正确 70% / 错误口令 20% / 不存在账号 10%）',
      refresh: 'POST /admin/v1/auth/refresh（有效 90% / 已轮换重放 10%，登录段含 MFA）',
      mfa: 'POST /admin/v1/auth/mfa/verify（TOTP 正确 / 错误各 50%）',
    }[scenario],
    channel,
    accounts: subjects.length,
    rate_rps: rate,
    duration: duration,
    samples,
    success_rate: samples > 0 ? round3((samples - errors) / samples) : 0,
    errors,
    skipped: skippedCount,
    http_req_failed_rate: round3(failedRate),
    p50_ms: round3(trend.med),
    p95_ms: round3(trend['p(95)']),
    p99_ms: round3(trend['p(99)']),
    max_ms: round3(trend.max),
    threshold_p95_ms: scenario === 'login' ? 300 : 500,
    threshold_p99_ms: scenario === 'login' ? 800 : null,
    p95_passed: trend['p(95)'] !== undefined
      && trend['p(95)'] <= (scenario === 'login' ? 300 : 500),
    elapsed_seconds: Math.round(elapsed * 10) / 10,
    started_at: new Date(Date.now() - elapsed * 1000).toISOString(),
  };

  const summary = [
    `identity.${scenario}: accounts=${subjects.length} rate=${rate}rps duration=${duration}`,
    `  samples=${samples} errors=${errors} skipped=${skippedCount} success=${(artifact.success_rate * 100).toFixed(3)}%`,
    `  p50=${artifact.p50_ms}ms p95=${artifact.p95_ms}ms p99=${artifact.p99_ms}ms max=${artifact.max_ms}ms`,
    `  elapsed=${artifact.elapsed_seconds}s p95_passed=${artifact.p95_passed}`,
  ].join('\n');

  const out = __ENV.ARTIFACT;
  if (!out) {
    return { stdout: summary + '\n' };
  }
  return { [out]: JSON.stringify(artifact, null, 2) + '\n', stdout: summary + '\n' };
}

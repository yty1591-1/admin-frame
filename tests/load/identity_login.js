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
//   BASE_URL（必填）、CHANNEL（admin|biz，默认 admin）、LOGIN_SUBJECTS（逗号分隔，必填）
//   PASSWORD（必填）、WRONG_PASSWORD（默认在正确密码后追加 #）、MERCHANT_ID（B 端可选）
//   TOTP_SECRET（MFA 场景必填）、RATE（每秒到达数，默认 50）、DURATION（默认 10m）
import crypto from 'k6/crypto';
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';

const requestedScenario = (process.env.SCENARIO || 'login').toLowerCase();
// 仅支持单场景运行，避免空指标导致阈值误判。
const scenario = requestedScenario === 'refresh' || requestedScenario === 'mfa' ? requestedScenario : 'login';
const baseURL = (process.env.BASE_URL || '').replace(/\/+$/, '');
const channel = (process.env.CHANNEL || 'admin').toLowerCase();
const subjects = (process.env.LOGIN_SUBJECTS || '').split(',').map((s) => s.trim()).filter(Boolean);
const password = process.env.PASSWORD || '';
const wrongPassword = process.env.WRONG_PASSWORD || `${password}#`;
const merchantID = process.env.MERCHANT_ID || '';
const totpSecret = process.env.TOTP_SECRET || '';
const rate = parseInt(process.env.RATE || '50', 10);
const duration = process.env.DURATION || '10m';

const loginDuration = new Trend('identity_login_duration', true);
const refreshDuration = new Trend('identity_refresh_duration', true);
const mfaDuration = new Trend('identity_mfa_duration', true);
const outcomes = new Counter('identity_flow_outcome');

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
};

function requiredEnv() {
  const missing = [];
  if (!baseURL) missing.push('BASE_URL');
  if (!subjects.length) missing.push('LOGIN_SUBJECTS');
  if (!password) missing.push('PASSWORD');
  if (scenario === 'mfa' && !totpSecret) missing.push('TOTP_SECRET');
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
}

// loginFlow 复现 SLO 文档的用户分布：正确密码 70%、错误密码 20%、不存在账号 10%。
export function loginFlow() {
  const roll = Math.random();
  const subject = subjects[Math.floor(Math.random() * subjects.length)];
  const credential = roll < 0.7 ? password : wrongPassword;
  const target = roll < 0.9 ? subject : `${subject}-missing`;

  const res = login(target, credential);
  loginDuration.add(res.timings.duration);

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
export function refreshFlow() {
  const subject = subjects[Math.floor(Math.random() * subjects.length)];
  const loginRes = login(subject, password);
  const data = envelope(loginRes).data || {};
  if (!data.refresh_token) {
    recordOutcome('refresh_login_failed');
    return;
  }
  const refresh = (token) => http.post(`${baseURL}/${channel}/v1/auth/refresh`, JSON.stringify({ refresh_token: token }), {
    headers: { 'Content-Type': 'application/json' },
    tags: { endpoint: 'refresh' },
  });

  const res = refresh(data.refresh_token);
  refreshDuration.add(res.timings.duration);
  const ok = check(res, {
    'refresh accepted': (r) => r.status === 200 && envelope(r).code === 0,
  });
  recordOutcome(ok ? 'refresh_success' : 'refresh_unexpected');

  if (Math.random() < 0.1) {
    // 旧令牌已被轮换：必须整链撤销并返回 40100。
    const replay = refresh(data.refresh_token);
    refreshDuration.add(replay.timings.duration);
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
  const code = expectSuccess ? totpCode(totpSecret) : '000000';
  const res = http.post(`${baseURL}/${channel}/v1/auth/mfa/verify`, JSON.stringify({ mfa_ticket: data.mfa_ticket, code }), {
    headers: { 'Content-Type': 'application/json' },
    tags: { endpoint: 'mfa_verify' },
  });
  mfaDuration.add(res.timings.duration);
  if (expectSuccess) {
    const ok = check(res, {
      'mfa accepted': (r) => r.status === 200 && envelope(r).code === 0 && !!envelope(r).data.access_token,
    });
    recordOutcome(ok ? 'mfa_success' : 'mfa_unexpected');
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

function totpCode(secret, digits = 6, period = 30) {
  const key = base32Decode(secret);
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

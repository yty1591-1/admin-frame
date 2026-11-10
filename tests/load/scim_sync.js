// SCIM 2.0 目录同步演练（AC-P16-SCIM-1：10k 用户 + 50 组 ≤ 30 min，错误率 < 0.1%）。
//
// 用法（staging，先经 A 端管理端点签发 SCIM 令牌）：
//   k6 run tests/load/scim_sync.js \
//     -e BASE_URL=https://admin.example.com -e SCIM_TOKEN=scim_xxx \
//     -e USERS=10000 -e GROUPS=50 -e PER_GROUP=200
//
// 幂等演练：同一批次再跑一次，创建会返回 409 uniqueness，
// 脚本把 409 计为「已存在」而不是失败——这正是 AC-P16-SCIM-2 要求的行为。
//
// 阶段划分：
//   1. 场景内并发创建用户（USERS 次迭代，VUS 并发）；
//   2. teardown 里串行创建组（GROUPS 个，每组成员引用上面已建的 externalId）。
//
// 为什么组放在 teardown：组成员按 externalId 解析，若与用户创建并发执行，
// 先到的组会引用尚未建出的用户而返回 400 invalidValue，产生假失败。
// teardown 在所有迭代结束后运行，成员必然已存在。总同步耗时 = 场景耗时 + teardown 耗时。
import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';

const BASE_URL = __ENV.BASE_URL || 'http://127.0.0.1:8080';
const TOKEN = __ENV.SCIM_TOKEN || '';
const USERS = parseInt(__ENV.USERS || '10000', 10);
const GROUPS = parseInt(__ENV.GROUPS || '50', 10);
const PER_GROUP = parseInt(__ENV.PER_GROUP || '200', 10);
const VUS = parseInt(__ENV.VUS || '8', 10);
const SYNC_BUDGET = __ENV.SYNC_BUDGET || '30m';

export const options = {
  scenarios: {
    scim_users: {
      executor: 'shared-iterations',
      vus: VUS,
      iterations: USERS,
      maxDuration: SYNC_BUDGET,
    },
  },
  // 让摘要里出现 p(99)：k6 默认只算 avg/min/med/max/p(90)/p(95)，
  // 未列入 summaryTrendStats 的分位数取不到值（产物会写 null）。
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
  // 组创建在 teardown：需要显式放宽上限，否则默认 60s 会截断。
  teardownTimeout: __ENV.TEARDOWN_TIMEOUT || '10m',
  thresholds: {
    // 错误率 < 0.1%（AC-P16-SCIM-1）；创作用 expectedStatuses(201, 409) 把
    // 「已存在」从失败里排除，否则幂等复跑必然击穿该阈值。
    http_req_failed: ['rate<0.001'],
    checks: ['rate>0.999'],
    // 只为让 k6 计算 p(99)（未被阈值引用的分位数不会出现在摘要里），
    // 同时兜住「个别请求卡死」；30s 不是 SLO 判据。
    http_req_duration: ['p(99)<30000'],
  },
};

const headers = {
  Authorization: `Bearer ${TOKEN}`,
  'Content-Type': 'application/scim+json',
};

// expectedStatuses 让 409 uniqueness 视为预期响应，不计入 http_req_failed。
const createStatuses = http.expectedStatuses(201, 409);

// externalId 固定生成，保证重复执行落到同一幂等键。
function externalID(index) {
  return `ext-${String(index).padStart(6, '0')}`;
}

function userName(index) {
  return `user-${String(index).padStart(6, '0')}@corp.example`;
}

function requireToken() {
  if (!TOKEN) {
    throw new Error('SCIM_TOKEN is required');
  }
}

export default function () {
  requireToken();
  const index = exec.scenario.iterationInTest;
  const response = http.post(
    `${BASE_URL}/admin/scim/v2/Users`,
    JSON.stringify({
      schemas: ['urn:ietf:params:scim:schemas:core:2.0:User'],
      externalId: externalID(index),
      userName: userName(index),
      name: { givenName: `User${index}`, familyName: 'Sync' },
      active: true,
    }),
    { headers, responseCallback: createStatuses },
  );
  check(response, {
    'user created or already exists': (r) => r.status === 201 || r.status === 409,
  });
}

// teardown 在全部用户迭代结束后运行，成员此时必然存在。
export function teardown() {
  requireToken();
  for (let groupIndex = 0; groupIndex < GROUPS; groupIndex++) {
    const members = [];
    for (let offset = 0; offset < PER_GROUP; offset++) {
      members.push({ value: externalID((groupIndex * PER_GROUP + offset) % USERS) });
    }
    const response = http.post(
      `${BASE_URL}/admin/scim/v2/Groups`,
      JSON.stringify({
        schemas: ['urn:ietf:params:scim:schemas:core:2.0:Group'],
        externalId: `grp-${String(groupIndex).padStart(3, '0')}`,
        displayName: `team-${groupIndex}`,
        members,
      }),
      { headers, responseCallback: createStatuses },
    );
    check(response, {
      'group created or already exists': (r) => r.status === 201 || r.status === 409,
    });
  }
}

// handleSummary 直接产出登记表（docs/perf/LOAD_BASELINES.md）要求的产物结构。
//
// 字段与 tests/load/artifacts/*.json 及 check-load-baseline 的 LB-03 断言对齐：
// samples / errors / success_rate / p95_ms 缺一即门禁失败。
// 输出路径用 -e ARTIFACT=/scripts/artifacts/scim-sync-10k.json 指定；
// 未指定时只打印摘要，不落盘（幂等复跑不制造游离产物）。
export function handleSummary(data) {
  // 保留 3 位小数：与 tests/load/artifacts 既有产物精度一致，避免浮点尾巴进登记表。
  const round3 = (value) => (typeof value === 'number' ? Math.round(value * 1000) / 1000 : value);
  const metrics = data.metrics || {};
  const valuesOf = (name) => (metrics[name] ? metrics[name].values : undefined) || {};
  const reqs = valuesOf('http_reqs');
  const duration = valuesOf('http_req_duration');
  const failedRate = metrics.http_req_failed ? metrics.http_req_failed.values.rate : 0;

  const samples = reqs.count || 0;
  const errors = Math.round(samples * failedRate);
  const elapsed = (data.state && data.state.testRunDurationMs ? data.state.testRunDurationMs : 0) / 1000;

  const artifact = {
    scenario: 'scim.sync',
    target: 'POST /admin/scim/v2/Users + /admin/scim/v2/Groups（SCIM Bearer + MySQL 事务 + 角色投影）',
    users: USERS,
    groups: GROUPS,
    members_per_group: PER_GROUP,
    vus: VUS,
    samples: samples,
    success_rate: samples > 0 ? round3((samples - errors) / samples) : 0,
    errors: errors,
    // k6 的中位数键是 med，p(50) 不一定存在。
    p50_ms: round3(duration.med),
    p95_ms: round3(duration['p(95)']),
    p99_ms: round3(duration['p(99)']),
    max_ms: round3(duration.max),
    throughput_rps: round3(reqs.rate),
    duration_seconds: Math.round(elapsed * 10) / 10,
    target_duration_seconds: 1800,
    passed: failedRate < 0.001,
    started_at: new Date(Date.now() - elapsed * 1000).toISOString(),
  };

  const summary = [
    `scim_sync: users=${USERS} groups=${GROUPS}×${PER_GROUP} vus=${VUS}`,
    `  samples=${samples} errors=${errors} success=${(artifact.success_rate * 100).toFixed(3)}%`,
    `  p50=${artifact.p50_ms}ms p95=${artifact.p95_ms}ms p99=${artifact.p99_ms}ms max=${artifact.max_ms}ms`,
    `  elapsed=${artifact.duration_seconds}s throughput=${artifact.throughput_rps.toFixed(1)}/s passed=${artifact.passed}`,
  ].join('\n');

  const out = __ENV.ARTIFACT;
  if (!out) {
    return { stdout: summary + '\n' };
  }
  return { [out]: JSON.stringify(artifact, null, 2) + '\n', stdout: summary + '\n' };
}

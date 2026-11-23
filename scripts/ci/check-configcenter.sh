#!/usr/bin/env bash
# 配置中心门禁：契约、权限、审计、敏感项与拉取模型不得回退。
#
# 背景：M0 冻结了附录 A 数据模型、§6.7 OpenAPI、权限点与审计事件；M1～M3
# 落地管理面、密钥子域与 ADR-0016 拉取模型。本脚本把「已冻结契约」变成
# 可机器校验的门禁，避免后续改动删表、删权限、漏审计或让敏感值进入拉取响应。
#
# 检查项：
#   CC-01 迁移 up / down 成对，六张 config_* 表齐全
#   CC-02 权限种子包含读 / 写 / 审批 / 发布四点
#   CC-03 审计动作包含配置变更、播种、发布、回滚、审批
#   CC-04 管理面 OpenAPI 文件存在且被 admin.yaml 引用
#   CC-05 拉取构建跳过敏感项，SDK 经 HTTP 拉取（ADR-0016）
#   CC-06 未声明键拒绝写入（spec 注册表校验存在）
#   CC-07 指标 / 告警 / runbook 资产存在
#   CC-08 config-api 拉取模型：HTTP 源、长轮询、本地缓存、ReleaseWatcher
#   CC-09 无文件启动：configboot + AllowMissingFiles；四进程接线
#   CC-10 业务域键声明：四个域 spec、默认值一致性测试、无整段 deferred
#   CC-11 多 namespace 消费与 Watch / 长轮询接线
#   CC-12 Helm 部署接线：values 默认值、环境变量与缓存挂载
#   CC-13 M2 密钥契约：secret 表迁移、OpenAPI、权限、审计、信封与 config-api
#   CC-14 M3 无 tracked configs、EnvAllowlist、JWT secretclient 冒烟
#   CC-15 排除理由对齐 CONFIG_STANDARDS §3.1.1；禁止过时「M1 暂不支持」；
#        HotKeySet / 声明键 hot_whitelist / Cancel 路由契约存在（CC-R-18）
#
# 用法: bash scripts/ci/check-configcenter.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

FAIL=0

fail() {
  echo "ERROR: $*" >&2
  FAIL=1
}

ok() {
  echo "    OK: $*"
}

# require_file <路径> <说明>
require_file() {
  if [[ -f "$1" ]]; then
    ok "$2"
  else
    fail "缺少 $2：$1"
  fi
}

# require_grep <文件> <正则> <说明>
require_grep() {
  if grep -Eq "$2" "$1"; then
    ok "$3"
  else
    fail "$3（未在 $1 匹配到 /$2/）"
  fi
}

echo "==> CC-01: migration pair and config tables"
require_file "migrations/000031_create_config_center.up.sql" "配置中心迁移 up"
require_file "migrations/000031_create_config_center.down.sql" "配置中心迁移 down"
if [[ -f "migrations/000031_create_config_center.up.sql" ]]; then
  tables="$(grep -Eo 'CREATE TABLE[^(]*config_[a-z_]+' \
    migrations/000031_create_config_center.up.sql | wc -l | tr -d ' ')"
  if [[ "${tables}" -ge 6 ]]; then
    ok "config_* 表数量 ${tables}（>= 6）"
  else
    fail "config_* 表数量 ${tables}，期望 >= 6"
  fi
fi

echo "==> CC-02: permission seeds"
# config:read / config:write 在 IAM 基线（000003）已存在；配置中心迁移补齐发布与审批四点。
for permission in config:read config:write config:approve config:publish; do
  if grep -REq "'${permission}'" migrations/*.up.sql; then
    ok "权限点 ${permission}"
  else
    fail "权限点 ${permission} 未在迁移中播种"
  fi
done

echo "==> CC-03: audit actions"
for action in config.change config.bootstrap.completed config.install.begin \
  config.install.apply config.install.complete config.install.fail \
  config.release.publish config.release.rollback config.release.approve; do
  require_grep "internal/shared/audit/actions.go" "\"${action}\"" "审计动作 ${action}"
done

echo "==> CC-04: OpenAPI contract"
require_file "api/openapi/paths/admin/configcenter.yaml" "配置中心 OpenAPI 路径文件"
if [[ -f "api/openapi/admin.yaml" ]]; then
  refs="$(grep -c "paths/admin/configcenter.yaml" api/openapi/admin.yaml || true)"
  if [[ "${refs}" -ge 10 ]]; then
    ok "admin.yaml 引用配置中心路径 ${refs} 处（>= 10）"
  else
    fail "admin.yaml 仅引用配置中心路径 ${refs} 处，期望 >= 10"
  fi
fi

echo "==> CC-05: pull excludes sensitive values (ADR-0016)"
require_grep "internal/configcenter/app/snapshot_service.go" \
  "item\.Sensitive" "拉取构建识别敏感项"
require_grep "internal/configcenter/app/snapshot_service.go" \
  "continue" "敏感项不写入拉取响应"
require_grep "pkg/configclient/http_source.go" \
  "NewHTTPSource" "SDK HTTP 拉取源"

echo "==> CC-06: undeclared keys are rejected"
require_grep "internal/configcenter/spec/registry.go" \
  "not declared in code key spec" "未声明键拒绝写入"

echo "==> CC-07: observability assets"
require_file "internal/configcenter/observability/metrics.go" "配置中心指标实现"
require_file "deploy/observability/prometheus/rules/configcenter.yaml" "配置中心告警规则"
require_file "docs/runbooks/config-center.md" "配置中心 runbook"
require_grep "docs/runbooks/ALERTS.md" "config\.client_stale" "告警目录登记 client_stale"

echo "==> CC-08: config-api pull model (ADR-0016)"
require_file "pkg/configclient/http_source.go" "HTTP 配置源"
require_file "pkg/configclient/long_poll.go" "长轮询客户端"
require_file "pkg/configclient/dir_cache.go" "本地缓存目录"
require_file "cmd/internal/configboot/load.go" "业务进程启动拉取"
require_grep "internal/configcenter/transport/rest/internal_handler.go" \
  "GetNamespaceConfig" "内部拉取 Handler"
require_grep "internal/configcenter/app/release_watcher.go" \
  "ReleaseWatcher" "pointer 变更扫描器"

echo "==> CC-09: boot via configboot"
require_grep "pkg/config/loader.go" "AllowMissingFiles" "无文件启动开关"
require_grep "cmd/internal/configboot/load.go" "CONFIG_API_URL" "启动引导变量"
booted="$(grep -l "configboot.Load" cmd/*/main.go 2>/dev/null | wc -l | tr -d ' ')"
if [[ "${booted}" -ge 4 ]]; then
  ok "四个业务进程均使用 configboot.Load（${booted}）"
else
  fail "仅 ${booted} 个进程使用 configboot.Load，期望 4 个"
fi

echo "==> CC-10: business domain key specs"
for namespace in identity audit tenant notification; do
  require_file "internal/configcenter/spec/${namespace}.go" "${namespace} 键声明"
done
require_file "cmd/internal/appconfig/spec_defaults_test.go" "业务默认值一致性测试"
if grep -q "deferredPrefixes" cmd/internal/appconfig/config_spec_coverage_test.go; then
  fail "业务域仍存在整段 deferred 前缀，须逐键声明或登记排除原因"
else
  ok "业务域已逐键声明或登记排除"
fi

echo "==> CC-11: multi-namespace + long poll"
require_grep "pkg/configclient/client.go" \
  "Namespaces \[\]string" "SDK 多 namespace 配置"
require_grep "cmd/internal/configcenterruntime/runtime.go" \
  "enabledNamespaces\(\)" "刷新客户端 namespace 解析"
require_grep "pkg/configclient/client.go" \
  "func \(c \*Client\) Watch" "SDK Watch 订阅"
require_grep "pkg/configclient/client.go" \
  "runLongPoll" "SDK 长轮询驱动"
require_grep "internal/configcenter/observability/metrics.go" \
  "config_client_watch_dropped_total" "Watch 丢事件指标"
for process in biz-api c-api worker; do
  require_grep "cmd/${process}/main.go" "NewRefresh" "${process} 动态刷新观测"
done
require_grep "cmd/config-api/main.go" "NewConfigAPI" "config-api 自举入口"
require_grep "cmd/config-api/main.go" \
  "/internal/v1/configs/notifications" "config-api 注册长轮询"
require_grep "cmd/config-api/main.go" \
  "/internal/v1/install/status" "config-api 注册安装态（ADR-0017）"
require_file "api/openapi/paths/internal/configs.yaml" "internal OpenAPI 拉取路径"
require_file "api/openapi/paths/internal/install.yaml" "internal OpenAPI 安装路径"
require_file "migrations/000037_create_config_install_state.up.sql" "安装态迁移 up"
require_grep "api/openapi/internal.yaml" \
  "internal-v1-configs-env-namespace" "internal OpenAPI 登记拉取路径"
require_grep "api/openapi/internal.yaml" \
  "internal-v1-install-status" "internal OpenAPI 登记安装态"
require_grep "api/openapi/internal.yaml" \
  "internal-v1-install-schema" "internal OpenAPI 登记向导 schema"
require_file "api/openapi/paths/admin/install.yaml" "admin 安装向导代理 OpenAPI 路径"
require_grep "api/openapi/admin.yaml" \
  "admin-v1-install-status" "admin OpenAPI 登记安装向导代理"
require_grep "api/openapi/admin.yaml" \
  "admin-v1-install-schema" "admin OpenAPI 登记向导 schema"
require_file "migrations/000038_config_install_repair_mode.up.sql" "安装态 repair 迁移 up"
require_file "migrations/000038_config_install_repair_mode.down.sql" "安装态 repair 迁移 down"
require_grep "migrations/000038_config_install_repair_mode.up.sql" \
  "repair_keys" "迁移新增 repair 白名单列（ADR-0018）"
require_grep "api/openapi/components.yaml" \
  "repair_keys" "安装态契约含 repair 白名单"
require_grep "api/openapi/schemas/internal/install.yaml" \
  "remaining_gaps" "apply 响应含剩余阻塞键"
require_file "internal/platform/setup_only.go" "启动降级态运行器（ADR-0018）"
require_grep "internal/platform/setup_only.go" \
  "SETUP-ONLY MODE: missing required keys" "降级态日志首行可 grep"
require_grep "cmd/admin-api/setup_only.go" \
  "runSetupOnly" "admin-api 缺键时降级装配"
require_grep "cmd/internal/configboot/gaps.go" \
  "func MissingRequired" "必填缺值判定（与 status.gaps 同源）"
require_grep "cmd/biz-api/main.go" \
  "configboot.RequireComplete" "B 端缺键 fail-fast"
require_grep "cmd/c-api/main.go" \
  "configboot.RequireComplete" "C 端缺键 fail-fast"
require_grep "cmd/worker/main.go" \
  "configboot.RequireComplete" "worker 缺键 fail-fast"
require_grep "internal/configcenter/app/install.go" \
  "repair only accepts frozen blocking keys" "repair 白名单越权拒绝"

echo "==> CC-12: Helm deployment wiring"
require_grep "deploy/helm/admin-frame/values.yaml" "configCenter:" "Helm configCenter 默认值"
require_grep "deploy/helm/admin-frame/templates/_workload.tpl" \
  "CONFIG_API_URL" "Helm 注入 config-api URL"
require_grep "deploy/helm/admin-frame/templates/_workload.tpl" \
  "CONFIG_SERVICE_IDENTITY" "Helm 注入服务身份"
require_grep "deploy/helm/admin-frame/templates/_workload.tpl" \
  "CONFIGCENTER_ENV" "Helm 注入配置环境"
require_grep "deploy/helm/admin-frame/templates/_workload.tpl" \
  "configcenter-cache" "Helm 挂载本地缓存 emptyDir"

echo "==> CC-13: M2 secret hosting contract"
require_file "migrations/000033_create_config_secrets.up.sql" "密钥表迁移 up"
require_file "migrations/000033_create_config_secrets.down.sql" "密钥表迁移 down"
if [[ -f "migrations/000033_create_config_secrets.up.sql" ]]; then
  for table in config_secret config_secret_version config_secret_grant config_secret_review; do
    require_grep "migrations/000033_create_config_secrets.up.sql" \
      "CREATE TABLE IF NOT EXISTS ${table}" "建表 ${table}"
  done
fi
for permission in 'config:secret:read' 'config:secret:write' 'config:secret:approve'; do
  if grep -REq "'${permission}'" migrations/*.up.sql; then
    ok "权限点 ${permission}"
  else
    fail "权限点 ${permission} 未在迁移中播种"
  fi
done
for action in config.secret.create config.secret.rotate config.secret.revoke \
  config.secret.grant config.secret.resolve; do
  require_grep "internal/shared/audit/actions.go" "\"${action}\"" "审计动作 ${action}"
done
require_file "api/openapi/paths/admin/secrets.yaml" "密钥管理 OpenAPI 路径"
require_file "api/openapi/schemas/admin/secrets.yaml" "密钥管理 OpenAPI schema"
require_file "api/openapi/internal.yaml" "config-api 内部 OpenAPI"
require_file "api/openapi/paths/internal/secrets.yaml" "内部 resolve 路径"
require_grep "api/openapi/admin.yaml" "paths/admin/secrets.yaml" "admin.yaml 引用密钥路径"
require_file "pkg/envelope/envelope.go" "信封加密实现"
require_file "pkg/secretclient/client.go" "密钥解析 SDK"
require_file "cmd/config-api/main.go" "config-api 入口"
require_file "docs/runbooks/secret-management.md" "密钥 runbook"
require_file "docs/runbooks/key-ceremony.md" "key ceremony"

echo "==> CC-14: zero tracked configs + EnvAllowlist + configboot + JWT secretclient"
tracked="$(git ls-files 'configs' 2>/dev/null || true)"
if [[ -z "${tracked}" ]]; then
  ok "git ls-files configs 为空"
else
  fail "M3 后仍有 tracked configs/ 文件"
  printf '%s\n' "${tracked}" >&2
fi
require_grep "pkg/config/loader.go" "EnvPathAllowlist" "Loader 支持环境变量路径白名单"
require_grep "pkg/config/env.go" "BindEnvFiltered" "BindEnv 支持白名单过滤"
require_grep "internal/platform/config/config.go" "InfraEnvPathAllowlist" "基础设施 env 白名单常量"
require_grep "cmd/internal/appconfig/appconfig.go" "InfraEnvPathAllowlist" "业务 Loader 挂载 EnvAllowlist"
require_grep "cmd/admin-api/main.go" "configboot.Load" "admin-api 拉取启动"
require_grep "cmd/biz-api/main.go" "configboot.Load" "biz-api 拉取启动"
require_grep "cmd/c-api/main.go" "configboot.Load" "c-api 拉取启动"
require_grep "cmd/worker/main.go" "configboot.Load" "worker 拉取启动"
if grep -RIn --exclude-dir=.git --exclude='check-configcenter.sh' --exclude='_*' \
  -E 'snapshotconfig\.RequireLoad|CONFIGCENTER_SNAPSHOT_|CONFIGCENTER_ALLOW_MISSING_CONFIG' \
  cmd pkg internal scripts/local docs/CONFIG_STANDARDS.md docs/ARCHITECTURE.md 2>/dev/null \
  | grep -vE 'ADR-0016|deprecated|已移除|BREAKING|升级说明' >/dev/null; then
  fail "仍存在快照签名启动残留"
  grep -RIn --exclude-dir=.git --exclude='check-configcenter.sh' --exclude='_*' \
    -E 'snapshotconfig\.RequireLoad|CONFIGCENTER_SNAPSHOT_|CONFIGCENTER_ALLOW_MISSING_CONFIG' \
    cmd pkg internal scripts/local docs/CONFIG_STANDARDS.md docs/ARCHITECTURE.md 2>/dev/null | head -n 40 >&2 || true
else
  ok "无 RequireLoad / 快照签名环境变量残留"
fi
require_grep "cmd/internal/secretruntime/jwt.go" "auth.jwt.private_key" "JWT 密钥消费 secretclient"
require_grep "cmd/internal/secretruntime/resolve.go" "CONFIG_API_URL and CONFIG_SERVICE_IDENTITY are required" \
  "secretclient 引导变量必填"
require_grep "cmd/admin-api/main.go" "jwt secret:" "admin-api JWT fail-fast"
require_file "docs/adr/0016-config-service-pull-model.md" "ADR-0016 拉取模型"
GO_BIN=""
if command -v go >/dev/null 2>&1; then
  GO_BIN="go"
elif command -v go.exe >/dev/null 2>&1; then
  GO_BIN="go.exe"
fi
if [[ -n "${GO_BIN}" ]]; then
  if "${GO_BIN}" test -count=1 ./cmd/internal/secretruntime/ -run 'TestApplyJWTSecret|TestNewFromEnv' >/dev/null; then
    ok "JWT secret resolve 冒烟"
  else
    fail "JWT secret resolve 冒烟失败"
  fi
else
  require_grep "cmd/internal/secretruntime/jwt_test.go" "TestApplyJWTSecret" \
    "JWT resolve 冒烟用例存在（本机 PATH 无 go，跳过执行）"
fi

echo "==> CC-15: exclusion reasons + Wave3 contracts (CC-R-18)"
require_grep "docs/CONFIG_STANDARDS.md" "3\.1\.1 永久排除" "CONFIG_STANDARDS 永久排除表"
require_grep "internal/configcenter/spec/hot.go" "var HotKeys" "ADR-0015 HotKeys 白名单"
require_grep "internal/configcenter/transport/rest/handler.go" "HotWhitelist" \
  "声明键响应含 hot_whitelist"
require_grep "api/openapi/schemas/admin/configcenter.yaml" "hot_whitelist" \
  "OpenAPI ConfigDeclaredKey.hot_whitelist"
require_grep "api/openapi/paths/admin/configcenter.yaml" \
  "admin-v1-configs-releases-by-id-cancel" "OpenAPI Release Cancel"
require_grep "internal/configcenter/transport/admin/router.go" \
  "releases/{id}/cancel" "管理面 Cancel 路由"
# 只扫 excludedKeys / excludedPrefixes 映射体，避开测试用例自身的禁止短语字面量。
if awk '
  /^var excluded(Keys|Prefixes)/ {in_map=1}
  in_map {
    print
    if (/^}$/) {in_map=0}
  }
' cmd/internal/appconfig/config_spec_coverage_test.go \
  | grep -Eq 'M1 暂不支持|暂不支持 float|当前值类型不支持|值类型不支持'; then
  fail "排除清单含过时理由（禁止 M1 暂不支持 / 值类型不支持 等）"
else
  ok "排除清单无过时理由"
fi
if [[ -n "${GO_BIN:-}" ]] || command -v go >/dev/null 2>&1 || command -v go.exe >/dev/null 2>&1; then
  if [[ -z "${GO_BIN:-}" ]]; then
    if command -v go >/dev/null 2>&1; then GO_BIN="go"; else GO_BIN="go.exe"; fi
  fi
  if "${GO_BIN}" test -count=1 ./cmd/internal/appconfig/ \
    -run 'TestExcludedReasonsForbidStalePhrases|TestPlatformKeyCoverage' >/dev/null; then
    ok "覆盖率与排除理由测试"
  else
    fail "覆盖率与排除理由测试失败"
  fi
else
  require_grep "cmd/internal/appconfig/config_spec_coverage_test.go" \
    "TestExcludedReasonsForbidStalePhrases" "排除理由测试存在（本机 PATH 无 go，跳过执行）"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "配置中心门禁失败（CC-01～CC-15）" >&2
  exit 1
fi

echo "配置中心门禁通过"

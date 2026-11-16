#!/usr/bin/env bash
# 配置中心 M1 门禁：契约、权限、审计、敏感项与快照验签不得回退。
#
# 背景：M0 冻结了附录 A 数据模型、§6.7 OpenAPI、权限点与审计事件；M1 落地
# 管理面与 SDK。本脚本把这些「已冻结契约」变成可机器校验的门禁，避免后续
# 改动删表、删权限、漏审计或让敏感值进入快照。
#
# 检查项：
#   CC-01 迁移 up / down 成对，六张 config_* 表齐全
#   CC-02 权限种子包含读 / 写 / 审批 / 发布四点
#   CC-03 审计动作包含配置变更、播种、发布、回滚、审批
#   CC-04 管理面 OpenAPI 文件存在且被 admin.yaml 引用
#   CC-05 快照/拉取构建跳过敏感项，SDK 经 HTTP 拉取（ADR-0016）
#   CC-06 未声明键拒绝写入（spec 注册表校验存在）
#   CC-07 指标 / 告警 / runbook 资产存在
#   CC-08 双读运行时接线：展平、对比入口、差异指标、四进程接入
#   CC-09 无文件启动：Loader 开关、演练环境变量、四进程接线
#   CC-10 业务域键声明：四个域 spec、默认值一致性测试、无整段 deferred
#   CC-11 多 namespace 消费与开关：文件列表、启用列表、SDK 与双读接线
#   CC-12 Helm 部署接线：values 默认值、环境变量与快照 Secret 挂载
#   CC-13 M2 密钥契约：secret 表迁移、OpenAPI、权限、审计、信封与 config-api
#   CC-14 M3 无 tracked configs、无文件启动开关与密钥 resolve 冒烟
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
for action in config.change config.bootstrap.completed config.release.publish \
  config.release.rollback config.release.approve; do
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
require_grep "api/openapi/internal.yaml" \
  "paths/internal/configs.yaml" "internal OpenAPI 登记拉取路径"

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

if [[ "${FAIL}" -ne 0 ]]; then
  echo "配置中心门禁失败（CC-01～CC-14）" >&2
  exit 1
fi

echo "配置中心门禁通过"

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
#   CC-05 快照构建跳过敏感项，SDK 装载前必须验签
#   CC-06 未声明键拒绝写入（spec 注册表校验存在）
#   CC-07 指标 / 告警 / runbook 资产存在
#   CC-08 双读运行时接线：展平、对比入口、差异指标、四进程接入
#   CC-09 无文件启动：Loader 开关、演练环境变量、四进程接线
#   CC-10 业务域键声明：四个域 spec、默认值一致性测试、无整段 deferred
#   CC-11 多 namespace 消费与开关：文件列表、启用列表、SDK 与双读接线
#   CC-12 Helm 部署接线：values 默认值、环境变量与快照 Secret 挂载
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

echo "==> CC-05: snapshot excludes sensitive values and verifies signature"
require_grep "internal/configcenter/app/snapshot_service.go" \
  "item\.Sensitive" "快照构建识别敏感项"
require_grep "internal/configcenter/app/snapshot_service.go" \
  "continue" "敏感项不写入快照"
require_grep "pkg/configclient/client.go" \
  "verifier\.Verify" "SDK 装载前验签"

echo "==> CC-06: undeclared keys are rejected"
require_grep "internal/configcenter/spec/registry.go" \
  "not declared in code key spec" "未声明键拒绝写入"

echo "==> CC-07: observability assets"
require_file "internal/configcenter/observability/metrics.go" "配置中心指标实现"
require_file "deploy/observability/prometheus/rules/configcenter.yaml" "配置中心告警规则"
require_file "docs/runbooks/config-center.md" "配置中心 runbook"
require_grep "docs/runbooks/ALERTS.md" "config\.client_stale" "告警目录登记 client_stale"

echo "==> CC-08: dual-read runtime wiring"
require_file "pkg/config/flat.go" "配置文件展平（双读基线）"
require_file "cmd/internal/snapshotconfig/dualread.go" "双读对比入口"
require_grep "internal/configcenter/observability/dualread.go" \
  "config_dual_read_mismatch" "双读差异指标"
wired="$(grep -l "ReportWithMetrics" cmd/*/main.go 2>/dev/null | wc -l | tr -d ' ')"
if [[ "${wired}" -ge 4 ]]; then
  ok "四个进程均接入启动期双读（${wired}）"
else
  fail "仅 ${wired} 个进程接入双读，期望 4 个"
fi

echo "==> CC-09: no-file startup support"
require_grep "pkg/config/loader.go" "AllowMissingFiles" "无文件启动开关"
require_grep "cmd/internal/snapshotconfig/startup.go" \
  "CONFIGCENTER_ALLOW_MISSING_CONFIG" "无文件启动演练环境变量"
allowed="$(grep -l "LoadWithValuesAllowMissing" cmd/*/main.go 2>/dev/null | wc -l | tr -d ' ')"
if [[ "${allowed}" -ge 4 ]]; then
  ok "四个进程均支持无文件启动（${allowed}）"
else
  fail "仅 ${allowed} 个进程支持无文件启动，期望 4 个"
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

echo "==> CC-11: multi-namespace consumption and switch"
require_grep "cmd/internal/snapshotconfig/startup.go" \
  "CONFIGCENTER_SNAPSHOT_FILES" "多 namespace 启动快照列表"
require_grep "cmd/internal/snapshotconfig/startup.go" \
  "CONFIGCENTER_ENABLED_NAMESPACES" "namespace 接管开关"
require_grep "pkg/configclient/client.go" \
  "Namespaces \[\]string" "SDK 多 namespace 配置"
require_grep "cmd/internal/configcenterruntime/runtime.go" \
  "EnabledNamespaces\(\)" "刷新客户端按开关加载 namespace"
require_grep "cmd/internal/snapshotconfig/dualread.go" \
  "ByNamespace" "双读按 namespace 对比"
require_grep "pkg/configclient/client.go" \
  "func \(c \*Client\) Watch" "SDK Watch 订阅"
require_grep "internal/configcenter/observability/metrics.go" \
  "config_client_watch_dropped_total" "Watch 丢事件指标"
require_grep "cmd/internal/configcenterruntime/runtime.go" \
  "OnWatchDropped" "Watch 指标接入运行时"
for process in biz-api c-api worker; do
  require_grep "cmd/${process}/main.go" "NewRefresh" "${process} 动态刷新观测"
done

echo "==> CC-12: Helm deployment wiring"
require_grep "deploy/helm/admin-frame/values.yaml" "configCenter:" "Helm configCenter 默认值"
require_grep "deploy/helm/admin-frame/templates/_workload.tpl" \
  "CONFIGCENTER_SNAPSHOT_FILES" "Helm 注入快照文件列表"
require_grep "deploy/helm/admin-frame/templates/_workload.tpl" \
  "CONFIGCENTER_ENABLED_NAMESPACES" "Helm 注入 namespace 开关"
require_grep "deploy/helm/admin-frame/templates/_workload.tpl" \
  "configcenter-snapshots" "Helm 挂载快照 Secret"

if [[ "${FAIL}" -ne 0 ]]; then
  echo "配置中心门禁失败（CC-01～CC-12）" >&2
  exit 1
fi

echo "配置中心门禁通过"

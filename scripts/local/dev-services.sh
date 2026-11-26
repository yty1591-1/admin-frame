#!/usr/bin/env bash
# 本地开发：一键启动 / 停止 / 重启 / 查看五进程（config-api + worker + 三端 API）。
#
# 用法：
#   bash scripts/local/dev-services.sh <start|stop|restart|status|install> [选项]
#
# 选项：
#   --no-build          跳过 go build
#   --skip-health       启动后不等待 /readyz（同时跳过安装步骤）
#   --env-file <path>   默认 scripts/local/.env（可从 env.example 复制）
#   --migrate           start/restart 前执行 migrate up
#
# 说明：
#   1. 启动顺序：config-api →（就绪）→ 安装向导（begin/apply/complete）→ worker → admin-api → biz-api → c-api
#   2. 所有环境统一走安装流程（ADR-0018）；`install` 子命令可单独重跑安装
#   3. 每个进程注入各自的 CONFIG_SERVICE_IDENTITY（密钥 resolve 授权）
#   4. PID / 日志写入 bin/run/（已被 /bin/ 忽略）
#   5. Windows 请用原生 Git Bash 或 make local-*；勿从 WSL 拉起 Windows 二进制
#   6. 破坏性冒烟（清空密钥表）见 scripts/local/smoke-start-all.ps1

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

# WSL bash 拉起 Windows PE 会丢环境变量且 pid 语义不一致 → 切 Git Bash。
if [[ -r /proc/version ]] && grep -qi microsoft /proc/version 2>/dev/null; then
  for candidate in "/mnt/c/Program Files/Git/bin/bash.exe" "/mnt/c/Program Files/Git/usr/bin/bash.exe"; do
    [[ -x "$candidate" ]] || continue
    script_win="$(wslpath -w "${BASH_SOURCE[0]}" 2>/dev/null || true)"
    script_win="${script_win//\\//}"
    [[ -n "$script_win" ]] || continue
    printf 'NOTE: 检测到 WSL bash，切换到 Git Bash 执行。\n'
    case "${1:-}" in
      start | restart)
        printf 'WARN: 请在原生 Git Bash / PowerShell(make) 中执行 start/restart，避免进程随 WSL 退出。\n'
        ;;
    esac
    exec "$candidate" "$script_win" "$@"
  done
  printf 'ERROR: 检测到 WSL bash，但未找到 Git Bash。\n' >&2
  exit 1
fi

GO="${GO:-go}"
BIN_DIR="${BIN_DIR:-bin}"
RUNTIME_DIR="${RUNTIME_DIR:-$BIN_DIR/run}"
PID_DIR="$RUNTIME_DIR/pids"
LOG_DIR="$RUNTIME_DIR/logs"
ENV_FILE="${ENV_FILE:-scripts/local/.env}"
NO_BUILD=0
SKIP_HEALTH=0
DO_MIGRATE=0
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-90}"
API_HOST="${API_HOST:-127.0.0.1}"
ADMIN_API_PORT="${ADMIN_API_PORT:-8081}"
BIZ_API_PORT="${BIZ_API_PORT:-8082}"
C_API_PORT="${C_API_PORT:-8083}"
CONFIG_API_PORT="${CONFIG_API_PORT:-8085}"
WORKER_PORT="${WORKER_PORT:-8090}"

# 启动顺序；config-api 必须最先就绪。
ROLES=(config-api worker admin-api biz-api c-api)

EXE=""
case "$(uname -s)" in
  *MINGW* | *MSYS* | *CYGWIN*) EXE=".exe" ;;
esac

identity_for() {
  case "$1" in
    config-api) printf '%s' "config-api" ;;
    worker) printf '%s' "worker" ;;
    admin-api) printf '%s' "admin-api" ;;
    biz-api) printf '%s' "biz-api" ;;
    c-api) printf '%s' "c-api" ;;
    *) printf '%s' "$1" ;;
  esac
}

binary_path() {
  local role="$1"
  if [[ -n "$EXE" && -f "$BIN_DIR/$role$EXE" ]]; then
    printf '%s/%s%s' "$BIN_DIR" "$role" "$EXE"
    return 0
  fi
  printf '%s/%s' "$BIN_DIR" "$role"
}

build_output() {
  printf '%s/%s%s' "$BIN_DIR" "$1" "$EXE"
}

log() { printf '%s\n' "$*"; }
warn() { printf 'WARN: %s\n' "$*" >&2; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  sed -n '2,20p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

parse_args() {
  local action="${1:-}"
  if [[ -z "$action" ]]; then
    usage
    exit 1
  fi
  shift || true
  case "$action" in
    start | stop | restart | status | install) ACTION="$action" ;;
    -h | --help | help) usage; exit 0 ;;
    *) die "未知子命令：$action（可用：start|stop|restart|status|install）" ;;
  esac
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --no-build) NO_BUILD=1 ;;
      --skip-health) SKIP_HEALTH=1 ;;
      --migrate) DO_MIGRATE=1 ;;
      --env-file)
        [[ $# -ge 2 ]] || die "--env-file 需要路径参数"
        ENV_FILE="$2"
        shift
        ;;
      -h | --help) usage; exit 0 ;;
      *) die "未知选项：$1" ;;
    esac
    shift
  done
}

load_env() {
  local file="$ENV_FILE"
  if [[ ! -f "$file" ]]; then
    if [[ -f scripts/local/env.example ]]; then
      die "缺少 $file；请先：cp scripts/local/env.example scripts/local/.env"
    fi
    die "环境变量文件不存在：$file"
  fi
  local line key value
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%$'\r'}"
    [[ -z "${line//[[:space:]]/}" ]] && continue
    [[ "$line" == \#* ]] && continue
    key="${line%%=*}"
    value="${line#*=}"
    key="${key//[[:space:]]/}"
    if [[ "$value" == \"*\" || "$value" == \'*\' ]]; then
      value="${value#?}"
      value="${value%?}"
    fi
    if [[ ! "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
      warn "跳过无法识别的环境变量行：${line:0:40}"
      continue
    fi
    export "$key=$value"
  done <"$file"
  # 保证引导变量有默认值（可被 .env 覆盖）。
  export CONFIG_API_URL="${CONFIG_API_URL:-http://127.0.0.1:8085}"
  export CONFIGCENTER_ENV="${CONFIGCENTER_ENV:-dev}"
  export CONFIGCENTER_CACHE_DIR="${CONFIGCENTER_CACHE_DIR:-var/configcenter-cache}"
  export CONFIGCENTER_KEK_DRIVER="${CONFIGCENTER_KEK_DRIVER:-local}"
  # 安装向导凭据：dev 缺省值仅用于本机自动安装（ADR-0018 统一安装流程）。
  export CONFIG_INSTALL_SETUP_TOKEN="${CONFIG_INSTALL_SETUP_TOKEN:-local-dev-install-setup-token-0123456789abcdef}"
  mkdir -p "$CONFIGCENTER_CACHE_DIR"
  log "已加载环境变量：$file"
}

build_all() {
  command -v "$GO" >/dev/null 2>&1 || die "未找到 go"
  mkdir -p "$BIN_DIR"
  local app
  for app in "${ROLES[@]}"; do
    log "构建 $app ..."
    "$GO" build -ldflags "-s -w" -o "$(build_output "$app")" "./cmd/$app"
  done
}

ensure_binaries() {
  local role bin missing=()
  for role in "${ROLES[@]}"; do
    bin="$(binary_path "$role")"
    [[ -f "$bin" ]] || missing+=("$bin")
  done
  if (( ${#missing[@]} > 0 )); then
    die "缺少二进制：${missing[*]}；去掉 --no-build 或先 make build"
  fi
}

pid_alive() {
  local pid="$1"
  [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null
}

read_pid() {
  local role="$1" f="$PID_DIR/$1.pid"
  [[ -f "$f" ]] || return 1
  local pid
  pid="$(cat "$f" 2>/dev/null || true)"
  [[ -n "$pid" ]] || return 1
  pid_alive "$pid" || return 1
  printf '%s' "$pid"
}

start_role() {
  local role="$1"
  local bin pidfile logfile pid identity
  bin="$(binary_path "$role")"
  [[ -f "$bin" ]] || die "二进制不存在：$bin"
  pidfile="$PID_DIR/$role.pid"
  logfile="$LOG_DIR/$role.log"
  identity="$(identity_for "$role")"

  if pid="$(read_pid "$role")"; then
    warn "$role 已在运行（pid=$pid），跳过"
    return 0
  fi

  export CONFIG_SERVICE_IDENTITY="$identity"
  printf '\n=== %s start %s identity=%s ===\n' "$role" "$(date '+%Y-%m-%d %H:%M:%S')" "$identity" >>"$logfile"
  nohup "$bin" </dev/null >>"$logfile" 2>&1 &
  pid=$!
  printf '%s' "$pid" >"$pidfile"

  sleep 0.3
  if ! pid_alive "$pid"; then
    warn "$role 启动即退出，最近日志："
    tail -n 30 "$logfile" >&2 || true
    rm -f "$pidfile"
    return 1
  fi
  log "$role 已启动（pid=$pid, identity=$identity, log=$logfile）"
}

health_one() {
  local url="$1"
  curl -fsS -o /dev/null --max-time 2 "$url" 2>/dev/null
}

wait_url() {
  local url="$1" seconds="${2:-$HEALTH_TIMEOUT}" i
  for ((i = 1; i <= seconds; i++)); do
    if health_one "$url"; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_ready() {
  [[ "$SKIP_HEALTH" -eq 1 ]] && return 0
  if ! command -v curl >/dev/null 2>&1; then
    warn "未找到 curl，跳过 /readyz 探测"
    return 0
  fi
  local i
  for ((i = 1; i <= HEALTH_TIMEOUT; i++)); do
    local ok=1
    health_one "http://$API_HOST:$CONFIG_API_PORT/readyz" || ok=0
    health_one "http://$API_HOST:$WORKER_PORT/readyz" || ok=0
    health_one "http://$API_HOST:$ADMIN_API_PORT/readyz" || ok=0
    health_one "http://$API_HOST:$BIZ_API_PORT/readyz" || ok=0
    health_one "http://$API_HOST:$C_API_PORT/readyz" || ok=0
    if [[ "$ok" -eq 1 ]]; then
      log "全部服务就绪（耗时 ${i}s）"
      return 0
    fi
    for role in "${ROLES[@]}"; do
      if ! read_pid "$role" >/dev/null; then
        if [[ "$i" -gt 2 ]]; then
          warn "$role 进程已退出，最近日志："
          tail -n 40 "$LOG_DIR/$role.log" >&2 || true
          return 1
        fi
      fi
    done
    sleep 1
  done
  warn "等待 /readyz 超时（${HEALTH_TIMEOUT}s）；见 $LOG_DIR"
  return 1
}

run_migrate() {
  log "执行 migrate up ..."
  if [[ ! -f "$(binary_path migrate)" ]] && [[ ! -f "$BIN_DIR/migrate$EXE" ]]; then
    "$GO" build -ldflags "-s -w" -o "$(build_output migrate)" ./cmd/migrate
  fi
  local mig
  mig="$(binary_path migrate)"
  [[ -f "$mig" ]] || mig="$BIN_DIR/migrate$EXE"
  "$mig" -dir migrations -action up
}

# install_field 从安装态读取字段（env/status/owner）；无值输出空串。
install_field() {
  local field="$1" body
  body="$(curl -fsS "http://$API_HOST:$CONFIG_API_PORT/internal/v1/install/status" 2>/dev/null || true)"
  printf '%s' "$body" | tr -d '\r' | grep -o "\"$field\":\"[^\"]*\"" | head -n 1 | cut -d'"' -f4 || true
}

# install_post <begin|apply|complete> <json>：输出响应体，非 2xx 返回非 0。
install_post() {
  local path="$1" payload="$2" body code
  body="$(curl -sS -X POST "http://$API_HOST:$CONFIG_API_PORT/internal/v1/install/$path" \
    -H 'Content-Type: application/json' \
    -H "X-Config-Install-Token: ${CONFIG_INSTALL_SETUP_TOKEN:-}" \
    -d "$payload" -w $'\n%{http_code}' 2>&1 || true)"
  code="${body##*$'\n'}"
  body="${body%$'\n'*}"
  [[ -n "$body" ]] && printf '%s\n' "$body"
  [[ "$code" == 2* ]]
}

# run_install 驱动安装向导：ready 时跳过，否则 begin → apply → complete。
# 所有环境统一（ADR-0018）；overrides 留空表示全部采用 key spec 默认值。
run_install() {
  if ! command -v curl >/dev/null 2>&1; then
    warn "未找到 curl，跳过安装步骤（可手工 POST /internal/v1/install/{begin,apply,complete}）"
    return 0
  fi
  if [[ -z "${CONFIG_INSTALL_SETUP_TOKEN:-}" ]]; then
    die "CONFIG_INSTALL_SETUP_TOKEN 未设置；见 scripts/local/env.example"
  fi
  local status owner="local-dev" resp
  status="$(install_field status)"
  if [[ "$status" == "ready" ]]; then
    log "安装态已 ready，跳过安装向导"
    return 0
  fi
  if [[ -n "$(install_field owner)" ]]; then
    owner="$(install_field owner)"
  fi
  log "安装向导：begin → apply → complete（env=${CONFIGCENTER_ENV:-dev}，状态=${status:-none}，owner=$owner）"
  if ! resp="$(install_post begin "{\"owner\":\"$owner\"}")"; then
    warn "install begin 失败：${resp:-（无响应体）}"
    return 1
  fi
  if ! resp="$(install_post apply "{\"owner\":\"$owner\",\"overrides\":{}}")"; then
    warn "install apply 失败：${resp:-（无响应体）}"
    return 1
  fi
  if ! resp="$(install_post complete "{\"owner\":\"$owner\"}")"; then
    warn "install complete 失败：${resp:-（无响应体）}"
    return 1
  fi
  status="$(install_field status)"
  if [[ "$status" != "ready" ]]; then
    warn "安装后状态仍为 ${status:-unknown}，请查看 config-api 日志与 /internal/v1/install/status"
    return 1
  fi
  log "安装完成（status=ready）"
}

do_install() {
  load_env
  if ! health_one "http://$API_HOST:$CONFIG_API_PORT/readyz"; then
    die "config-api 未就绪；请先执行 start"
  fi
  run_install
}

do_start() {
  mkdir -p "$PID_DIR" "$LOG_DIR"
  load_env
  if [[ "$NO_BUILD" -eq 0 ]]; then
    build_all
  fi
  ensure_binaries
  if [[ "$DO_MIGRATE" -eq 1 ]]; then
    run_migrate
  fi

  if ! start_role config-api; then
    do_stop
    exit 1
  fi
  if [[ "$SKIP_HEALTH" -eq 0 ]]; then
    if ! wait_url "http://$API_HOST:$CONFIG_API_PORT/readyz" "$HEALTH_TIMEOUT"; then
      warn "config-api 未就绪，最近日志："
      tail -n 40 "$LOG_DIR/config-api.log" >&2 || true
      do_stop
      exit 1
    fi
    log "config-api ready"
    if ! run_install; then
      warn "安装未完成，停止本次启动的进程"
      do_stop
      exit 1
    fi
  fi

  local role
  for role in worker admin-api biz-api c-api; do
    if ! start_role "$role"; then
      warn "启动失败，回滚"
      do_stop
      exit 1
    fi
    [[ "$role" == "worker" ]] && sleep 1
  done
  if ! wait_ready; then
    warn "就绪检查未通过，停止本次启动的进程"
    do_stop
    exit 1
  fi
  log "启动完成。状态：bash scripts/local/dev-services.sh status；日志：$LOG_DIR"
}

stop_role() {
  local role="$1" pidfile="$PID_DIR/$1.pid" pid i
  [[ -f "$pidfile" ]] || return 0
  pid="$(cat "$pidfile" 2>/dev/null || true)"
  if [[ -z "$pid" ]] || ! pid_alive "$pid"; then
    rm -f "$pidfile"
    return 0
  fi
  log "停止 $role（pid=$pid）..."
  kill "$pid" 2>/dev/null || true
  for ((i = 0; i < 30; i++)); do
    pid_alive "$pid" || break
    sleep 0.5
  done
  if pid_alive "$pid"; then
    warn "$role 未在 15s 内退出，强制结束"
    kill -9 "$pid" 2>/dev/null || true
  fi
  rm -f "$pidfile"
}

do_stop() {
  # 逆序停：先业务后 config-api
  local role
  for role in c-api biz-api admin-api worker config-api; do
    stop_role "$role"
  done
  log "全部服务已停止"
}

do_status() {
  local role pid
  for role in "${ROLES[@]}"; do
    if pid="$(read_pid "$role")"; then
      printf '%-10s running  pid=%-8s identity=%s\n' "$role" "$pid" "$(identity_for "$role")"
    else
      printf '%-10s stopped\n' "$role"
    fi
  done
  printf '\n日志目录：%s\n' "$LOG_DIR"
  if command -v curl >/dev/null 2>&1; then
    printf '安装态：%s\n' "$(install_field status)"
  fi
}

parse_args "$@"
case "$ACTION" in
  start) do_start ;;
  stop) do_stop ;;
  restart) do_stop; do_start ;;
  status) do_status ;;
  install) do_install ;;
esac

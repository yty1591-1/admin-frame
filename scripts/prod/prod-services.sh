#!/usr/bin/env bash
# 本机 prod 演练：一键启动 / 停止 / 重启 / 查看五进程 / 仅迁移。
#
# 用法：
#   bash scripts/prod/prod-services.sh <start|stop|restart|status|migrate> [选项]
#
# 选项：
#   --no-build          跳过 go build
#   --skip-health       启动后不等待 /readyz
#   --env-file <path>   默认 scripts/prod/.env（可从 env.example 复制）
#   --migrate           start/restart 前执行 migrate up
#   --strict            任一进程失败即回滚全部（默认降级启动）
#
# 说明：
#   1. 最小启动：config-api ready 后拉起业务进程；不自动 begin/apply/complete
#   2. 业务配置请用 Admin /setup/install + CONFIG_INSTALL_SETUP_TOKEN 手动完成
#   3. 未安装完成时业务 /readyz 为 503（ADR-0017），不因此回滚启动
#   4. 每个进程注入 CONFIG_SERVICE_IDENTITY 与唯一 ADMIN_FRAME_ID_NODE_ID
#   5. config-api 健康检查走 mTLS /readyz（Git Bash Schannel curl 无法加载 PEM，改用 python ssl）
#   6. Windows / WSL：优先转调 prod-services.ps1；Bash 回退路径用 tasklist/taskkill 识别 Windows PID
#   7. 默认降级启动：config-api 是硬前置；其余进程失败只记录并保持已起进程，
#      便于打开 Admin /setup/install 修配置；--strict 恢复全有或全无
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

# Windows / WSL：生命周期命令转调原生 PowerShell，避免 Git Bash kill -0 认不出 Start-Process PID。
reexec_windows_ps1() {
  local ps_exe="" ps1_win action="$1"
  shift || true
  case "$(uname -s 2>/dev/null)" in
    *MINGW* | *MSYS* | *CYGWIN*) ;;
    *)
      if [[ -r /proc/version ]] && grep -qi microsoft /proc/version 2>/dev/null; then
        :
      else
        return 1
      fi
      ;;
  esac
  if command -v powershell.exe >/dev/null 2>&1; then
    ps_exe="$(command -v powershell.exe)"
  elif command -v powershell >/dev/null 2>&1; then
    ps_exe="$(command -v powershell)"
  else
    return 1
  fi
  if command -v cygpath >/dev/null 2>&1; then
    ps1_win="$(cygpath -w "$ROOT/scripts/prod/prod-services.ps1")"
  elif command -v wslpath >/dev/null 2>&1; then
    ps1_win="$(wslpath -w "$ROOT/scripts/prod/prod-services.ps1")"
  else
    ps1_win="$ROOT/scripts/prod/prod-services.ps1"
  fi
  local ps_args=("$action")
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --no-build) ps_args+=("-NoBuild") ;;
      --skip-health) ps_args+=("-SkipHealth") ;;
      --migrate) ps_args+=("-Migrate") ;;
      --strict) ps_args+=("-Strict") ;;
      --env-file)
        [[ $# -ge 2 ]] || return 1
        local ef="$2"
        if command -v cygpath >/dev/null 2>&1; then
          ef="$(cygpath -w "$ef")"
        elif command -v wslpath >/dev/null 2>&1; then
          ef="$(wslpath -w "$ef")"
        fi
        ps_args+=("-EnvFile" "$ef")
        shift
        ;;
      -h | --help) return 1 ;;
      *) return 1 ;;
    esac
    shift
  done
  printf 'NOTE: Windows/WSL 下转调 prod-services.ps1（可靠的进程保活与 PID 管理）。\n'
  exec "$ps_exe" -NoProfile -ExecutionPolicy Bypass -File "$ps1_win" "${ps_args[@]}"
}

if [[ $# -ge 1 ]]; then
  case "$1" in
    start | stop | restart | status | migrate)
      reexec_windows_ps1 "$@" || true
      ;;
  esac
fi

if [[ -r /proc/version ]] && grep -qi microsoft /proc/version 2>/dev/null; then
  for candidate in "/mnt/c/Program Files/Git/bin/bash.exe" "/mnt/c/Program Files/Git/usr/bin/bash.exe"; do
    [[ -x "$candidate" ]] || continue
    script_win="$(wslpath -w "${BASH_SOURCE[0]}" 2>/dev/null || true)"
    script_win="${script_win//\\//}"
    [[ -n "$script_win" ]] || continue
    printf 'WARN: 未找到 powershell.exe，改用 Git Bash；Windows 上请优先 make prod-start 或 prod-services.ps1。\n' >&2
    exec "$candidate" "$script_win" "$@"
  done
  printf 'ERROR: 检测到 WSL bash，但未找到 powershell.exe / Git Bash。\n' >&2
  exit 1
fi

GO="${GO:-go}"
BIN_DIR="${BIN_DIR:-bin}"
RUNTIME_DIR="${RUNTIME_DIR:-$BIN_DIR/run-prod}"
PID_DIR="$RUNTIME_DIR/pids"
LOG_DIR="$RUNTIME_DIR/logs"
ENV_FILE="${ENV_FILE:-scripts/prod/.env}"
NO_BUILD=0
SKIP_HEALTH=0
DO_MIGRATE=0
STRICT=0
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-120}"
API_HOST="${API_HOST:-127.0.0.1}"
ADMIN_API_PORT="${ADMIN_API_PORT:-8081}"
BIZ_API_PORT="${BIZ_API_PORT:-8082}"
C_API_PORT="${C_API_PORT:-8083}"
CONFIG_API_PORT="${CONFIG_API_PORT:-8085}"
WORKER_PORT="${WORKER_PORT:-8090}"

ROLES=(config-api worker admin-api biz-api c-api)

EXE=""
case "$(uname -s)" in
  *MINGW* | *MSYS* | *CYGWIN*) EXE=".exe" ;;
esac

identity_for() {
  printf '%s' "$1"
}

node_id_for() {
  case "$1" in
    config-api) printf '1' ;;
    admin-api) printf '2' ;;
    biz-api) printf '3' ;;
    c-api) printf '4' ;;
    worker) printf '5' ;;
    *) printf '1' ;;
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
  sed -n '2,23p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

parse_args() {
  local action="${1:-}"
  if [[ -z "$action" ]]; then
    usage
    exit 1
  fi
  shift || true
  case "$action" in
    start | stop | restart | status | migrate) ACTION="$action" ;;
    -h | --help | help) usage; exit 0 ;;
    *) die "未知子命令：$action（可用：start|stop|restart|status|migrate）" ;;
  esac
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --no-build) NO_BUILD=1 ;;
      --skip-health) SKIP_HEALTH=1 ;;
      --migrate) DO_MIGRATE=1 ;;
      --strict) STRICT=1 ;;
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
    die "缺少 $file；请先：cp scripts/prod/env.example scripts/prod/.env"
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

  # file 存储 Vault 的 root token 由 vault-init 写入；优先覆盖 .env 占位值。
  if [[ -f var/prod-vault/root.token ]]; then
    export VAULT_TOKEN
    VAULT_TOKEN="$(tr -d '[:space:]' <var/prod-vault/root.token)"
  fi

  export CONFIGCENTER_ENV="${CONFIGCENTER_ENV:-prod}"
  export CONFIGCENTER_CACHE_DIR="${CONFIGCENTER_CACHE_DIR:-var/configcenter-cache-prod}"
  export CONFIGCENTER_KEK_DRIVER="${CONFIGCENTER_KEK_DRIVER:-vault}"
  export CONFIG_API_URL="${CONFIG_API_URL:-https://127.0.0.1:8085}"
  export CONFIG_API_MTLS="${CONFIG_API_MTLS:-require}"
  mkdir -p "$CONFIGCENTER_CACHE_DIR" var/prod-storage

  [[ "$CONFIGCENTER_ENV" == "prod" ]] || die "CONFIGCENTER_ENV 必须为 prod（当前：$CONFIGCENTER_ENV）"
  [[ "$CONFIGCENTER_KEK_DRIVER" == "vault" ]] || die "prod 演练要求 CONFIGCENTER_KEK_DRIVER=vault"
  [[ "$CONFIG_API_MTLS" == "require" ]] || die "prod 演练要求 CONFIG_API_MTLS=require"
  [[ -n "${VAULT_ADDR:-}" && -n "${VAULT_TOKEN:-}" ]] || die "缺少 VAULT_ADDR / VAULT_TOKEN（先 make prod-vault-init）"
  [[ -n "${CONFIG_API_TLS_CERT:-}" && -f "$CONFIG_API_TLS_CERT" ]] || die "缺少服务端证书：$CONFIG_API_TLS_CERT（先 make prod-certs）"
  [[ -n "${CONFIG_API_CLIENT_CERT:-}" && -f "$CONFIG_API_CLIENT_CERT" ]] || die "缺少客户端证书：$CONFIG_API_CLIENT_CERT"
  [[ -n "${CONFIG_API_NAMESPACE_ALLOWLIST:-}" ]] || die "缺少 CONFIG_API_NAMESPACE_ALLOWLIST"
  [[ -n "${CONFIG_BOOTSTRAP_GRANT_IDENTITY:-}" ]] || die "缺少 CONFIG_BOOTSTRAP_GRANT_IDENTITY（prod CC-R-05）"
  # 对象存储连接配置由配置中心提供（安装向导 / 管理面审批），不再要求 env。
  # admin-api 在存储不可用时会降级为 setup-only，可经 /setup/install 修复。
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

# Windows Start-Process 返回的是宿主 PID；MSYS kill -0 看不到，须用 tasklist。
pid_alive() {
  local pid="$1"
  [[ -n "$pid" ]] || return 1
  if [[ -n "$EXE" ]] && command -v tasklist.exe >/dev/null 2>&1; then
    tasklist.exe /FI "PID eq $pid" /NH 2>/dev/null | grep -Eq "[^0-9]$pid[^0-9]|^$pid[^0-9]|[^0-9]$pid$"
  else
    kill -0 "$pid" 2>/dev/null
  fi
}

kill_pid() {
  local pid="$1" force="${2:-0}"
  if [[ -n "$EXE" ]] && command -v taskkill.exe >/dev/null 2>&1; then
    if [[ "$force" -eq 1 ]]; then
      taskkill.exe /PID "$pid" /T /F >/dev/null 2>&1 || true
    else
      # Windows 无 SIGTERM 对等物；先尝试无 /F，失败再强制。
      taskkill.exe /PID "$pid" >/dev/null 2>&1 || taskkill.exe /PID "$pid" /T /F >/dev/null 2>&1 || true
    fi
  else
    if [[ "$force" -eq 1 ]]; then
      kill -9 "$pid" 2>/dev/null || true
    else
      kill "$pid" 2>/dev/null || true
    fi
  fi
}

tail_role_logs() {
  local role="$1" lines="${2:-40}"
  local logfile="$LOG_DIR/$role.log" errfile="$LOG_DIR/$role.err.log"
  if [[ -f "$logfile" ]]; then
    warn "$role stdout（最近 ${lines} 行）："
    tail -n "$lines" "$logfile" >&2 || true
  fi
  if [[ -f "$errfile" ]]; then
    warn "$role stderr（最近 ${lines} 行）："
    tail -n "$lines" "$errfile" >&2 || true
  fi
}

read_pid() {
  local role="$1" f="$PID_DIR/$1.pid"
  [[ -f "$f" ]] || return 1
  local pid
  pid="$(tr -d '[:space:]' <"$f" 2>/dev/null || true)"
  [[ -n "$pid" ]] || return 1
  pid_alive "$pid" || return 1
  printf '%s' "$pid"
}

start_role() {
  local role="$1"
  local bin pidfile logfile errfile pid identity node_id
  local saved_vault_token="${VAULT_TOKEN:-}"
  bin="$(binary_path "$role")"
  [[ -f "$bin" ]] || die "二进制不存在：$bin"
  mkdir -p "$PID_DIR" "$LOG_DIR"
  pidfile="$PID_DIR/$role.pid"
  logfile="$LOG_DIR/$role.log"
  errfile="$LOG_DIR/$role.err.log"
  identity="$(identity_for "$role")"
  node_id="$(node_id_for "$role")"

  if pid="$(read_pid "$role")"; then
    warn "$role 已在运行（pid=$pid），跳过"
    return 0
  fi

  export CONFIG_SERVICE_IDENTITY="$identity"
  export ADMIN_FRAME_ID_NODE_ID="$node_id"
  # config-api（安装/密钥）与 admin-api（配置管理面）需要 Vault Transit KEK；
  # 其余业务进程不注入 token。
  if [[ "$role" == "config-api" || "$role" == "admin-api" ]]; then
    [[ -n "$saved_vault_token" ]] && export VAULT_TOKEN="$saved_vault_token"
  else
    unset VAULT_TOKEN || true
  fi
  # mTLS=require：身份取自客户端证书 CN，须按进程使用独立证书。
  local client_cert="var/prod-mtls/client-${role}.crt"
  local client_key="var/prod-mtls/client-${role}.key"
  if [[ -f "$client_cert" && -f "$client_key" ]]; then
    export CONFIG_API_CLIENT_CERT="$client_cert"
    export CONFIG_API_CLIENT_KEY="$client_key"
  fi

  # Start-Process -RedirectStandard* 会截断目标文件，横幅写入独立 meta，避免被冲掉。
  printf '=== %s start %s identity=%s node_id=%s ===\n' \
    "$role" "$(date '+%Y-%m-%d %H:%M:%S')" "$identity" "$node_id" >"$LOG_DIR/$role.meta.log"

  if [[ -n "$EXE" ]] && command -v powershell.exe >/dev/null 2>&1; then
    local win_bin win_log win_err win_cwd
    win_bin="$(cygpath -w "$bin" 2>/dev/null || printf '%s' "$bin")"
    win_log="$(cygpath -w "$logfile" 2>/dev/null || printf '%s' "$logfile")"
    win_err="$(cygpath -w "$errfile" 2>/dev/null || printf '%s' "$errfile")"
    win_cwd="$(cygpath -w "$ROOT" 2>/dev/null || printf '%s' "$ROOT")"
    pid="$(powershell.exe -NoProfile -Command \
      "\$p = Start-Process -FilePath '$win_bin' -WorkingDirectory '$win_cwd' \
        -RedirectStandardOutput '$win_log' -RedirectStandardError '$win_err' \
        -PassThru -WindowStyle Hidden; Write-Output \$p.Id")"
    pid="$(printf '%s' "$pid" | tr -d '[:space:]')"
    [[ -n "$pid" ]] || die "$role Start-Process 未返回 pid"
  else
    printf '\n=== %s start %s identity=%s node_id=%s ===\n' \
      "$role" "$(date '+%Y-%m-%d %H:%M:%S')" "$identity" "$node_id" >>"$logfile"
    nohup "$bin" </dev/null >>"$logfile" 2>&1 &
    pid=$!
  fi
  printf '%s' "$pid" >"$pidfile"

  # 恢复 token，供后续角色启动逻辑（仅 config-api 会再次注入）。
  if [[ -n "$saved_vault_token" ]]; then
    export VAULT_TOKEN="$saved_vault_token"
  fi

  sleep 0.8
  if ! pid_alive "$pid"; then
    warn "$role 启动即退出，最近日志："
    tail_role_logs "$role" 40
    rm -f "$pidfile"
    return 1
  fi
  log "$role 已启动（pid=$pid, identity=$identity, node_id=$node_id）"
}

health_scheme_host() {
  # config-api 强制 HTTPS；业务进程默认仍监听明文（TLS 仅挂在 config-api）。
  case "$1" in
    config-api) printf 'https' ;;
    *) printf 'http' ;;
  esac
}

# Git Bash 自带 curl 多为 Schannel，无法 import PEM 客户端证书（0x80092002）。
# 用 python ssl 做 mTLS /readyz；业务进程仍用 curl/http。
mtls_readyz() {
  local url="$1" ca="$2" cert="$3" key="$4" py=""
  if command -v python3 >/dev/null 2>&1; then
    py="$(command -v python3)"
  elif command -v python >/dev/null 2>&1; then
    py="$(command -v python)"
  else
    return 1
  fi
  "$py" -c 'import ssl,sys,urllib.request
url,ca,cert,key=sys.argv[1:5]
ctx=ssl.create_default_context(cafile=ca)
ctx.load_cert_chain(certfile=cert,keyfile=key)
urllib.request.urlopen(url,context=ctx,timeout=2).read()
' "$url" "$ca" "$cert" "$key" >/dev/null 2>&1
}

health_one() {
  local role="$1" port="$2" scheme url
  scheme="$(health_scheme_host "$role")"
  url="$scheme://$API_HOST:$port/readyz"
  if [[ "$scheme" == "https" ]]; then
    # mTLS=require：探测必须带客户端证书（健康检查固定用 CN=config-api）。
    local ca="${CONFIG_API_TLS_CA:-}"
    local probe_cert="var/prod-mtls/client-config-api.crt"
    local probe_key="var/prod-mtls/client-config-api.key"
    if [[ ! -f "$probe_cert" || ! -f "$probe_key" ]]; then
      probe_cert="${CONFIG_API_CLIENT_CERT:-}"
      probe_key="${CONFIG_API_CLIENT_KEY:-}"
    fi
    if [[ -n "$ca" && -f "$ca" && -n "$probe_cert" && -f "$probe_cert" && -n "$probe_key" && -f "$probe_key" ]]; then
      mtls_readyz "$url" "$ca" "$probe_cert" "$probe_key"
    else
      return 1
    fi
  else
    curl -fsS -o /dev/null --max-time 2 "$url" 2>/dev/null
  fi
}

wait_url_role() {
  local role="$1" port="$2" seconds="${3:-$HEALTH_TIMEOUT}" i
  for ((i = 1; i <= seconds; i++)); do
    if ! read_pid "$role" >/dev/null; then
      if [[ "$i" -gt 2 ]]; then
        warn "$role 进程已退出"
        return 1
      fi
    elif health_one "$role" "$port"; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_ready() {
  # 仅强等 config-api；业务未安装时 /readyz 503 属预期，返回 0 并打 warn。
  [[ "$SKIP_HEALTH" -eq 1 ]] && return 0
  if ! wait_url_role config-api "$CONFIG_API_PORT" "$HEALTH_TIMEOUT"; then
    warn "config-api /readyz 超时；见 $LOG_DIR"
    return 1
  fi
  local role port
  for role in worker admin-api biz-api c-api; do
    case "$role" in
      worker) port="$WORKER_PORT" ;;
      admin-api) port="$ADMIN_API_PORT" ;;
      biz-api) port="$BIZ_API_PORT" ;;
      c-api) port="$C_API_PORT" ;;
    esac
    if health_one "$role" "$port"; then
      log "$role /readyz ok"
    else
      warn "$role /readyz 未通过（install 未 ready 时属预期）"
    fi
  done
  return 0
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

do_migrate_only() {
  load_env
  run_migrate
  log "migrate up 完成"
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

  # config-api 是硬前置：它起不来，其余进程与安装向导都不可用，直接回滚。
  if ! start_role config-api; then
    do_stop
    exit 1
  fi
  if [[ "$SKIP_HEALTH" -eq 0 ]]; then
    if ! wait_url_role config-api "$CONFIG_API_PORT" "$HEALTH_TIMEOUT"; then
      warn "config-api 未就绪，最近日志："
      tail_role_logs config-api 50
      do_stop
      exit 1
    fi
    log "config-api ready（最小启动；业务播种请打开 Admin /setup/install）"
  fi

  local role
  for role in worker admin-api biz-api c-api; do
    start_role "$role" || warn "$role 启动失败，继续启动其余进程"
    [[ "$role" == "worker" ]] && sleep 1
  done

  # 未完成安装时业务 /readyz 为 503（ADR-0017），不因此失败启动；仅确认进程仍存活。
  # 默认降级启动：失败进程只汇总，不停已起进程，避免「一处配置错 → Admin 也进不去」。
  local failed=()
  for role in worker admin-api biz-api c-api; do
    if ! read_pid "$role" >/dev/null; then
      warn "$role 进程已退出，最近日志："
      tail_role_logs "$role" 50
      failed+=("$role")
    fi
  done

  if (( ${#failed[@]} > 0 )); then
    if [[ "$STRICT" -eq 1 ]]; then
      warn "以下进程未启动：${failed[*]}（--strict：回滚全部进程）"
      do_stop
      exit 1
    fi
    warn "以下进程未启动：${failed[*]}；其余进程保持运行"
    if ! read_pid admin-api >/dev/null; then
      warn "admin-api 未运行，安装向导不可用；请按日志修正配置后重试：make prod-restart"
    else
      warn "admin-api 可用，可打开 Admin /setup/install 补齐配置后重启失败进程"
    fi
    log "降级启动完成。状态：bash scripts/prod/prod-services.sh status；日志：$LOG_DIR"
    exit 1
  fi

  warn "业务 /readyz 在 install 未 ready 前可为 503；请打开 Admin /setup/install 完成播种"
  log "启动完成。状态：bash scripts/prod/prod-services.sh status；日志：$LOG_DIR"
}

stop_role() {
  local role="$1" pidfile="$PID_DIR/$1.pid" pid i
  [[ -f "$pidfile" ]] || return 0
  pid="$(tr -d '[:space:]' <"$pidfile" 2>/dev/null || true)"
  if [[ -z "$pid" ]] || ! pid_alive "$pid"; then
    rm -f "$pidfile"
    return 0
  fi
  log "停止 $role（pid=$pid）..."
  kill_pid "$pid" 0
  for ((i = 0; i < 30; i++)); do
    pid_alive "$pid" || break
    sleep 0.5
  done
  if pid_alive "$pid"; then
    warn "$role 未在 15s 内退出，强制结束"
    kill_pid "$pid" 1
  fi
  rm -f "$pidfile"
}

do_stop() {
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
      printf '%-10s running  pid=%-8s identity=%s node_id=%s\n' \
        "$role" "$pid" "$(identity_for "$role")" "$(node_id_for "$role")"
    else
      printf '%-10s stopped\n' "$role"
    fi
  done
  printf '\n日志目录：%s\n' "$LOG_DIR"
}

parse_args "$@"
case "$ACTION" in
  start) do_start ;;
  stop) do_stop ;;
  restart) do_stop; do_start ;;
  status) do_status ;;
  migrate) do_migrate_only ;;
esac

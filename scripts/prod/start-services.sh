#!/usr/bin/env bash
# 以生产配置启动三端 + worker 的本地/预发编排脚本。
#
# 用法：
#   bash scripts/prod/start-services.sh <start|stop|restart|status> [选项]
#
# 选项：
#   --no-build          跳过 go build，直接使用 bin/ 下已有二进制
#   --skip-health       启动后不等待 /readyz 就绪（仅用于排障）
#   --env-file <path>   环境变量文件；默认 configs/prod/.env（本地未跟踪；无回退样例）
#
# 说明：
#   1. 业务配置按 ADR-0016 从 config-api 拉取 + 本地缓存；仓库不跟踪 configs/。
#   2. --env-file 只应导出：ADMIN_FRAME_DB_* / ADMIN_FRAME_REDIS_*、
#      CONFIG_API_URL / CONFIG_SERVICE_IDENTITY / CONFIGCENTER_ENV（及可选
#      CONFIGCENTER_CACHE_DIR / CONFIGCENTER_NAMESPACES）、CONFIG_API_* TLS/mTLS。
#      禁止导出 ADMIN_FRAME_AUTH_JWT_SECRET 等业务密钥或业务配置覆盖。
#   3. 迁移不在本脚本内自动执行；生产必须先跑独立 migrate Job：
#        go run ./cmd/migrate -dsn "$ADMIN_FRAME_DB_DSN" -action up
#   4. 启动顺序固定为 config-api -> worker -> admin -> biz -> c；
#      首次部署必须先起 config-api（播种 + 拉取入口）。
#   5. PIDs 与日志写入 bin/run/（已被 /bin/ 忽略），不会污染工作区。
#   6. Windows 下若 bash 来自 WSL，脚本会自动切到 Git Bash：WSL 启动原生 Windows
#      进程时只透传 WSLENV 白名单，导出的 ADMIN_FRAME_* 会丢失。
#   7. 首个管理员由迁移 000005 创建（账号 + 密码凭据 + system_admin 角色 + 绑定）：
#        首次登录强制改密，再按 A 端策略完成 MFA 绑定；无需额外的 seed CLI。
#   8. Windows 下 start/restart 必须在原生 Git Bash 窗口执行，不要从 WSL 终端调用：
#      互操作层拉起的 Git Bash 持有隐藏控制台，脚本退出后控制台关闭，服务会收到
#      CTRL_CLOSE_EVENT（Go 运行时映射为 SIGTERM）并被优雅停机，日志表现为启动成功
#      后立刻出现 "shutdown requested" → "server stopped"。stop/status 不受此限制；
#      WSL 下的自动切换只能保住环境变量，无法保住进程生命周期。
#   9. 无本地 yaml 时使用 unused 路径；业务进程 configboot.Load 拉取失败且无缓存则退出。

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

# 若当前 bash 来自 WSL，则导出的环境变量与 pid 语义都不适用于原生 Windows 进程：
# WSL 只会把 WSLENV 白名单里的变量透传给 PE，kill -0 也无法识别 Windows pid。
# 因此在 Windows 仓库下自动改用 Git Bash（MSYS）执行，保证单一路径、行为一致。
if [[ -r /proc/version ]] && grep -qi microsoft /proc/version 2>/dev/null; then
  for candidate in "/mnt/c/Program Files/Git/bin/bash.exe" "/mnt/c/Program Files/Git/usr/bin/bash.exe"; do
    [[ -x "$candidate" ]] || continue
    script_win="$(wslpath -w "${BASH_SOURCE[0]}" 2>/dev/null || true)"
    script_win="${script_win//\\//}"
    [[ -n "$script_win" ]] || continue
    printf 'NOTE: 检测到 WSL bash，切换到 Git Bash 执行（Windows 进程需要原生环境变量）。\n'
    case "${1:-}" in
      start | restart)
        printf 'WARN: WSL 调用下 start/restart 的服务会随本进程退出而停止；请在原生 Git Bash 中执行，见说明第 8 条。\n'
        ;;
    esac
    exec "$candidate" "$script_win" "$@"
  done
  printf 'ERROR: 检测到 WSL bash，但未找到 Git Bash；请在 PowerShell 中显式使用 Git Bash，或安装 Git for Windows。\n' >&2
  exit 1
fi

GO="${GO:-go}"
BIN_DIR="${BIN_DIR:-bin}"
RUNTIME_DIR="${RUNTIME_DIR:-$BIN_DIR/run}"
PID_DIR="$RUNTIME_DIR/pids"
LOG_DIR="$RUNTIME_DIR/logs"
ENV_FILE="${ENV_FILE:-configs/prod/.env}"
NO_BUILD=0
SKIP_HEALTH=0
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-60}"
API_HOST="${API_HOST:-127.0.0.1}"
ADMIN_API_PORT="${ADMIN_API_PORT:-8081}"
BIZ_API_PORT="${BIZ_API_PORT:-8082}"
C_API_PORT="${C_API_PORT:-8083}"
CONFIG_API_PORT="${CONFIG_API_PORT:-8085}"

ROLES=(config-api worker admin-api biz-api c-api)
# role -> 配置中的端别，用于拼接默认配置文件名。
ROLE_KEY_config_api="config"
ROLE_KEY_worker="worker"
ROLE_KEY_admin_api="admin"
ROLE_KEY_biz_api="biz"
ROLE_KEY_c_api="c"

EXE=""
case "$(uname -s)" in
  *MINGW* | *MSYS* | *CYGWIN*) EXE=".exe" ;;
esac

# bin/<role>[.exe] 的实际路径：Windows 下优先 .exe，同时兼容以无扩展名
# 构建的产物（WSL / 交叉编译），避免误用残留的旧二进制。
binary_path() {
  local role="$1"
  if [[ -n "$EXE" && -f "$BIN_DIR/$role$EXE" ]]; then
    printf '%s/%s%s' "$BIN_DIR" "$role" "$EXE"
    return 0
  fi
  printf '%s/%s' "$BIN_DIR" "$role"
}

log() { printf '%s\n' "$*"; }
warn() { printf 'WARN: %s\n' "$*" >&2; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  sed -n '2,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

parse_args() {
  local action="${1:-}"
  if [[ -z "$action" ]]; then
    usage
    exit 1
  fi
  shift || true
  case "$action" in
    start | stop | restart | status) ACTION="$action" ;;
    -h | --help | help) usage; exit 0 ;;
    *) die "未知子命令：$action（可用：start|stop|restart|status）" ;;
  esac
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --no-build) NO_BUILD=1 ;;
      --skip-health) SKIP_HEALTH=1 ;;
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
    die "环境变量文件不存在：$ENV_FILE（M3 已删除 env.example；请自建未跟踪的 .env 或传入 --env-file）"
  fi
  local line key value
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%$'\r'}"
    [[ -z "${line//[[:space:]]/}" ]] && continue
    [[ "$line" == \#* ]] && continue
    key="${line%%=*}"
    value="${line#*=}"
    key="${key//[[:space:]]/}"
    # 允许成对引号包裹值（常见 dotenv 写法）。这里不执行 source，不剥离的话
    # 引号会成为 DSN / 口令的一部分，例如 DSN 用户名会变成 '"root'。
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
  log "已加载环境变量：$file"
}

resolve_config() {
  local role="$1" key
  key="ROLE_KEY_${role//-/_}"
  key="${!key}"
  local real="configs/prod-${key}.yaml"
  if [[ -f "$real" ]]; then
    printf '%s' "$real"
    return 0
  fi
  # 无本地 yaml：返回占位路径；业务进程经 configboot 从 config-api 拉取。
  warn "未找到 $real；将依赖 CONFIG_API_URL 拉取（无 yaml 回退）"
  printf '%s' "unused.yaml"
}

# build_output 是构建产物的目标路径；Windows 下统一带 .exe 后缀。
build_output() {
  printf '%s/%s%s' "$BIN_DIR" "$1" "$EXE"
}

build_all() {
  command -v "$GO" >/dev/null 2>&1 || die "未找到 go，无法构建；可加 --no-build 使用已有二进制"
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
    die "缺少二进制：${missing[*]}；先执行 make build 或去掉 --no-build 由脚本构建"
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
  local config bin pidfile logfile pid
  config="$(resolve_config "$role")"
  bin="$(binary_path "$role")"
  [[ -f "$bin" ]] || die "二进制不存在：$bin（先执行 make build 或去掉 --no-build）"
  pidfile="$PID_DIR/$role.pid"
  logfile="$LOG_DIR/$role.log"

  if pid="$(read_pid "$role")"; then
    warn "$role 已在运行（pid=$pid），跳过"
    return 0
  fi

  printf '\n=== %s start %s ===\n' "$role" "$(date '+%Y-%m-%d %H:%M:%S')" >>"$logfile"
  nohup "$bin" -config "$config" </dev/null >>"$logfile" 2>&1 &
  pid=$!
  printf '%s' "$pid" >"$pidfile"

  sleep 0.3
  if ! pid_alive "$pid"; then
    warn "$role 启动即退出，最近日志："
    tail -n 20 "$logfile" >&2 || true
    rm -f "$pidfile"
    return 1
  fi
  log "$role 已启动（pid=$pid, config=$config, log=$logfile）"
}

health_one() {
  local url="$1"
  curl -fsS -o /dev/null --max-time 2 "$url" 2>/dev/null
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
    read_pid worker >/dev/null || ok=0
    health_one "http://$API_HOST:$ADMIN_API_PORT/readyz" || ok=0
    health_one "http://$API_HOST:$BIZ_API_PORT/readyz" || ok=0
    health_one "http://$API_HOST:$C_API_PORT/readyz" || ok=0
    if [[ "$ok" -eq 1 ]]; then
      log "全部服务就绪（耗时 ${i}s）"
      return 0
    fi
    for role in "${ROLES[@]}"; do
      if ! pid="$(read_pid "$role")"; then
        if [[ "$i" -gt 1 ]]; then
          warn "$role 进程已退出，最近日志："
          tail -n 30 "$LOG_DIR/$role.log" >&2 || true
          return 1
        fi
      fi
    done
    sleep 1
  done
  warn "等待 /readyz 超时（${HEALTH_TIMEOUT}s）；常见原因：DSN/Redis/对象存储不可达，见 $LOG_DIR"
  return 1
}

do_start() {
  mkdir -p "$PID_DIR" "$LOG_DIR"
  load_env
  if [[ "$NO_BUILD" -eq 0 ]]; then
    build_all
  fi
  ensure_binaries
  local role
  for role in "${ROLES[@]}"; do
    if ! start_role "$role"; then
      warn "启动失败，回滚本次已启动进程"
      do_stop
      exit 1
    fi
    # worker 先起，给 Outbox Relay 一个抢跑窗口。
    [[ "$role" == "worker" ]] && sleep 1
  done
  if ! wait_ready; then
    warn "就绪检查未通过，停止本次启动的进程"
    do_stop
    exit 1
  fi
  log "启动完成。查看状态：bash scripts/prod/start-services.sh status；日志：$LOG_DIR"
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
  local role
  for role in "${ROLES[@]}"; do
    stop_role "$role"
  done
  log "全部服务已停止"
}

do_status() {
  local role pid
  for role in "${ROLES[@]}"; do
    if pid="$(read_pid "$role")"; then
      printf '%-10s running  pid=%-8s config=%s\n' "$role" "$pid" "$(resolve_config "$role" 2>/dev/null || echo '?')"
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
esac

#!/usr/bin/env bash
# MySQL DSN 解析（供 backup.sh / restore.sh / verify.sh 共用）。
#
# 为什么需要独立文件：本项目的 DSN 是 Go 驱动格式
#   user:pass@tcp(host:3306)/db?parseTime=true&loc=UTC
# 而 MySQL 官方 CLI（mysqldump / mysql）只接受 --host/--port/--user 参数或
# 「位置参数 = 数据库名」。把 DSN 直接当成 mysql 的位置参数会退化为尝试本地
# socket 连接（ERROR 2002），导致恢复与载入校验不可用。
#
# 支持的格式：
#   1. mysql://user:pass@host:port/db?params      （官方 URL 风格）
#   2. user:pass@tcp(host:port)/db?params         （项目内部格式，ADMIN_FRAME_DB_DSN）
#
# 解析结果写入全局 DB_HOST / DB_PORT / DB_USER / DB_PASS / DB_NAME，
# 并把口令导出到 MYSQL_PWD，避免出现在命令行参数里（SEC-05）。
#
# 已知限制：口令中含未编码的 `@tcp(` 字面量时解析会错位；实际部署中口令为
# 字母数字随机串，且 go-sql-driver 本身也要求此类字符做 URL 编码，故不额外处理。

# parse_mysql_dsn <dsn>
#   成功返回 0 并填充全局变量；无法识别或缺少必需字段时返回 1。
parse_mysql_dsn() {
  local dsn="${1:-}"
  DB_HOST=""
  DB_PORT="3306"
  DB_USER=""
  DB_PASS=""
  DB_NAME=""

  if [[ "${dsn}" =~ ^mysql:// ]]; then
    local body="${dsn#mysql://}"
    local userinfo hostpart hostport namepart
    userinfo="${body%%@*}"
    hostpart="${body#*@}"
    DB_USER="${userinfo%%:*}"
    if [[ "${userinfo}" == *:* ]]; then
      DB_PASS="${userinfo#*:}"
    fi
    hostport="${hostpart%%/*}"
    namepart="${hostpart#*/}"
    DB_NAME="${namepart%%\?*}"
    DB_HOST="${hostport%%:*}"
    DB_PORT="${hostport#*:}"
    [[ "${DB_PORT}" == "${DB_HOST}" ]] && DB_PORT=3306
  elif [[ "${dsn}" == *"@tcp("* ]]; then
    # 项目格式：user[:pass]@tcp(host[:port])/db[?params]
    local userinfo rest addr tail
    userinfo="${dsn%@tcp(*}"
    rest="${dsn#*@tcp(}"
    addr="${rest%%)*}"
    tail="${rest#*)}"
    DB_USER="${userinfo%%:*}"
    if [[ "${userinfo}" == *:* ]]; then
      DB_PASS="${userinfo#*:}"
    fi
    DB_HOST="${addr%%:*}"
    DB_PORT="${addr#*:}"
    [[ "${DB_PORT}" == "${DB_HOST}" ]] && DB_PORT=3306
    DB_NAME="${tail#/}"
    DB_NAME="${DB_NAME%%\?*}"
  else
    return 1
  fi

  if [[ -z "${DB_HOST}" || -z "${DB_USER}" || -z "${DB_NAME}" ]]; then
    return 1
  fi
  [[ -n "${DB_PORT}" ]] || DB_PORT=3306
  export MYSQL_PWD="${DB_PASS}"
  return 0
}

# mysql_conn_args 输出可直接展开的连接参数数组名（bash 4 起支持 nameref）。
#
# 用法: local -a conn=(); mysql_conn_args conn; mysql "${conn[@]}" "${DB_NAME}"
mysql_conn_args() {
  local -n out="$1"
  out=(--host="${DB_HOST}" --port="${DB_PORT}" --user="${DB_USER}" --default-character-set=utf8mb4)
}

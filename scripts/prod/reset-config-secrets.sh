#!/usr/bin/env bash
# 清空本机 prod 演练库中的配置中心密钥表，便于换 KEK 后由 config-api 重新播种。
#
# 用法：bash scripts/prod/reset-config-secrets.sh
# 仅针对 docker-compose.prod-rehearsal.yml 的 MySQL（admin_frame_prod）。
# 请先 make prod-stop，避免 config-api 在 truncate 期间仍读写密文。

set -euo pipefail

CONTAINER="${PROD_MYSQL_CONTAINER:-admin-frame-prod-rehearsal-mysql-1}"
DB="${PROD_MYSQL_DB:-admin_frame_prod}"
MYSQL_USER="${PROD_MYSQL_USER:-root}"
MYSQL_PASSWORD="${PROD_MYSQL_PASSWORD:-root}"

if [[ "${RESET_CONFIG_SECRETS_CONFIRM:-}" != "1" ]]; then
  printf '将 TRUNCATE %s 中 config_secret* 表。确认请设置 RESET_CONFIG_SECRETS_CONFIRM=1 后重跑。\n' "$DB" >&2
  exit 1
fi

if ! docker exec "$CONTAINER" mysqladmin ping -h127.0.0.1 -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" --silent >/dev/null 2>&1; then
  printf 'ERROR: MySQL 容器未就绪：%s\n' "$CONTAINER" >&2
  exit 1
fi

docker exec -e MYSQL_PWD="$MYSQL_PASSWORD" "$CONTAINER" mysql -u"$MYSQL_USER" "$DB" -e "
SET FOREIGN_KEY_CHECKS=0;
TRUNCATE TABLE config_secret_grant;
TRUNCATE TABLE config_secret_review;
TRUNCATE TABLE config_secret_version;
TRUNCATE TABLE config_secret;
SET FOREIGN_KEY_CHECKS=1;
"

printf 'OK: 已清空 %s 中 config_secret* 表，下次启动 config-api 将重新播种密钥。\n' "$DB"

#!/usr/bin/env bash
# 兼容入口：转调 scripts/prod/prod-services.sh（本机 prod 演练主脚本）。
#
# 用法：
#   bash scripts/prod/start-services.sh <start|stop|restart|status|migrate> [选项]
#
# 默认读取 scripts/prod/.env。Windows/WSL 下 prod-services.sh 会再转调 prod-services.ps1。
# 若仍使用旧路径 configs/prod/.env，请显式传入：
#   bash scripts/prod/prod-services.sh start --env-file configs/prod/.env

set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
exec bash "$ROOT/scripts/prod/prod-services.sh" "$@"

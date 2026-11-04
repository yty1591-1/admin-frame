#!/usr/bin/env bash
# 值班呼叫链路自检（P1 必须可达；占位地址会被识别）。
# 用法:
#   bash scripts/ops/paging-probe.sh --labels severity=P1 --expect-receiver pager
#   ALERTMANAGER_URL=https://am.example.com bash scripts/ops/paging-probe.sh --post --labels severity=P1
# 说明：默认拒绝占位地址（*.invalid / example.com）；离线核对剧本时加 --allow-placeholder。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

if command -v python3 >/dev/null 2>&1; then
  PY=python3
elif command -v python >/dev/null 2>&1; then
  PY=python
else
  echo "ERROR: 需要 python3 或 python 运行 paging probe" >&2
  exit 2
fi

exec "${PY}" scripts/ops/paging_probe.py "$@"

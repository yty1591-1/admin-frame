#!/usr/bin/env bash
# 安装 CI 门禁所需的本地 Python 依赖（目前仅 PyYAML，供 OC-03 解析告警路由）。
# 优先复用已有解释器；需要安装时优先仓库内 .venv，避免污染受 PEP 668 管理的系统 Python。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

REQ="scripts/ci/requirements.txt"
PY_BIN="${PYTHON:-python3}"

# 当前解释器已满足依赖时直接结束。
if "${PY_BIN}" -c "import yaml" >/dev/null 2>&1; then
  echo "ci-deps: ${PY_BIN} 已具备 PyYAML，无需安装"
  exit 0
fi

if command -v uv >/dev/null 2>&1; then
  uv venv .venv --quiet --seed
  uv pip install --python .venv --quiet -r "${REQ}"
elif [[ -x ".venv/Scripts/python.exe" ]]; then
  .venv/Scripts/python.exe -m pip install --quiet -r "${REQ}"
elif [[ -x ".venv/bin/python3" ]]; then
  .venv/bin/python3 -m pip install --quiet -r "${REQ}"
else
  "${PY_BIN}" -m pip install --quiet -r "${REQ}"
fi

echo "ci-deps: Python 依赖已就绪（check-oncall.sh 将优先使用 .venv 解释器）"

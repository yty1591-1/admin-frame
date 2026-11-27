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
  echo "ci-deps: Python 依赖已就绪（check-oncall.sh 将优先使用 .venv 解释器）"
  exit 0
fi

# 无 uv 时必须用仓库内 .venv：Debian 12 / Ubuntu 24.04 及 GitHub、Gitea 的
# runner 镜像均按 PEP 668 标记系统 Python 为 externally-managed，
# `pip install` 会被直接拒绝（2026-11-26 Gitea `arch` job 实测：
# error: externally-managed-environment）。venv 是唯一不破坏系统包的正路。
if [[ ! -x ".venv/Scripts/python.exe" && ! -x ".venv/bin/python3" ]]; then
  echo "ci-deps: 创建仓库内 .venv（避免 PEP 668 拒绝系统级 pip 安装）"
  if ! "${PY_BIN}" -m venv .venv; then
    # 精简镜像可能缺少 ensurepip/python3-venv；此时退到 PEP 668 的显式豁免开关，
    # 只在容器/CI 这类一次性的 runner 内使用，不影响开发机。
    echo "ci-deps: venv 创建失败（缺 ensurepip？），回退 --break-system-packages" >&2
    "${PY_BIN}" -m pip install --quiet --break-system-packages -r "${REQ}"
    echo "ci-deps: Python 依赖已就绪（系统解释器 + --break-system-packages）"
    exit 0
  fi
fi

if [[ -x ".venv/Scripts/python.exe" ]]; then
  .venv/Scripts/python.exe -m pip install --quiet -r "${REQ}"
else
  .venv/bin/python3 -m pip install --quiet -r "${REQ}"
fi

echo "ci-deps: Python 依赖已就绪（check-oncall.sh 将优先使用 .venv 解释器）"

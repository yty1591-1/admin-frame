#!/usr/bin/env bash
# SEC-01：扫描工作区与 git 历史中的密钥。版本钉扎，禁止 latest。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

GITLEAKS_VERSION="${GITLEAKS_VERSION:-v8.24.2}"
export PATH="${HOME}/go/bin:/usr/local/go/bin:${PATH}"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

echo "==> gitleaks ${GITLEAKS_VERSION}"
# v8.24.2 的 module path 仍是 zricethezav（后续才迁到 gitleaks/gitleaks）。
exec go run "github.com/zricethezav/gitleaks/v8@${GITLEAKS_VERSION}" detect \
  --source . \
  --config .gitleaks.toml \
  --redact \
  --no-banner

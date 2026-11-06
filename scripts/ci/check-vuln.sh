#!/usr/bin/env bash
# CI-06 / DEP-04：govulncheck 门禁（支持受控例外）。
#
# 例外来源：deps/vuln-allowlist.txt（<OSV> <到期日> <ADR>）。
# 仅放行「本仓库代码可达」（=== Symbol Results ===）且已登记的漏洞；
# 未登记或已过期的漏洞仍然阻断；工具本身执行失败不视为通过。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ALLOWLIST="${ROOT}/deps/vuln-allowlist.txt"

cd "$ROOT"

out="$(mktemp)"
trap 'rm -f "$out"' EXIT

read -r -a tool <<< "${GOVULNCHECK:-go run golang.org/x/vuln/cmd/govulncheck@v1.1.4}"

set +e
"${tool[@]}" ./... >"${out}" 2>&1
rc=$?
set -e

if [ "${rc}" -eq 0 ]; then
	echo "OK: govulncheck 未发现代码可达漏洞"
	exit 0
fi

# govulncheck 以 3 表示「发现漏洞」；通过 go run 调用时外层会转成 1 并输出
# "exit status 3"。其他退出码是工具/环境异常，必须暴露。
if [ "${rc}" -ne 3 ] && ! grep -q "exit status 3" "${out}"; then
	cat "${out}"
	echo "ERROR: govulncheck 执行失败（exit ${rc}）" >&2
	exit 1
fi

ids="$(awk '/^=== Symbol Results ===/{f=1;next} /^=== /{f=0} f && /^Vulnerability #/{print $NF}' "${out}" | sort -u)"

if [ -z "${ids}" ]; then
	cat "${out}"
	echo "ERROR: govulncheck 失败但未解析到代码可达漏洞编号" >&2
	exit 1
fi

unapproved=""
for id in ${ids}; do
	entry="$(awk -v id="${id}" '$1 == id && $0 !~ /^#/ {print; exit}' "${ALLOWLIST}" 2>/dev/null || true)"
	if [ -z "${entry}" ]; then
		unapproved="${unapproved} ${id}(未登记)"
		continue
	fi
	expiry="$(printf '%s\n' "${entry}" | awk '{print $2}')"
	adr="$(printf '%s\n' "${entry}" | awk '{print $3}')"
	if ! expiry_ts="$(date -d "${expiry}" +%s 2>/dev/null)"; then
		unapproved="${unapproved} ${id}(到期日非法:${expiry})"
		continue
	fi
	if [ "${expiry_ts}" -lt "$(date +%s)" ]; then
		unapproved="${unapproved} ${id}(已过期:${expiry})"
		continue
	fi
	echo "WARN: ${id} 已登记为受控例外（${adr:-ADR 未填}，到期 ${expiry}）"
done

if [ -n "${unapproved}" ]; then
	cat "${out}"
	echo "ERROR: 未获批准的漏洞:${unapproved}" >&2
	exit 1
fi

echo "OK: 代码可达漏洞均在受控例外内"

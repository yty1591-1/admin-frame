#!/usr/bin/env bash
# 生成本机 prod 演练用自签 CA / server / 各进程 client 证书到 var/prod-mtls/。
#
# 用法：bash scripts/prod/gen-mtls-certs.sh
# mTLS=require 时服务身份取自客户端证书（URI SAN > DNS > CN），
# 因此每个进程必须有独立证书，CN 与 CONFIG_SERVICE_IDENTITY / grant 白名单一致。
#
# Git Bash 注意：勿全局 export MSYS_NO_PATHCONV——否则 Windows openssl 打不开 /d/... 路径。
# 主体名用 -config 写入，避免 -subj "/CN=..." 被 MSYS 路径转换破坏。

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="${PROD_MTLS_DIR:-$ROOT/var/prod-mtls}"
mkdir -p "$OUT"

ROLES=(config-api admin-api biz-api c-api worker)

need_regen=0
if [[ "${FORCE_REGEN:-0}" == "1" ]]; then
  need_regen=1
elif [[ ! -f "$OUT/ca.crt" || ! -f "$OUT/server.crt" ]]; then
  need_regen=1
else
  for role in "${ROLES[@]}"; do
    if [[ ! -f "$OUT/client-${role}.crt" || ! -f "$OUT/client-${role}.key" ]]; then
      need_regen=1
      break
    fi
  done
fi

if [[ "$need_regen" -eq 0 ]]; then
  printf 'OK: 已存在 %s（FORCE_REGEN=1 可强制重生成；重生成后须重启全部五进程）\n' "$OUT"
  exit 0
fi

command -v openssl >/dev/null 2>&1 || { printf 'ERROR: 需要 openssl\n' >&2; exit 1; }

write_req_cnf() {
  local path="$1"
  local cn="$2"
  cat >"$path" <<EOF
[req]
distinguished_name = dn
x509_extensions = v3
prompt = no

[dn]
CN = ${cn}

[v3]
basicConstraints = CA:FALSE
EOF
}

openssl genrsa -out "$OUT/ca.key" 2048
cat >"$OUT/ca.cnf" <<'EOF'
[req]
distinguished_name = dn
x509_extensions = v3_ca
prompt = no

[dn]
CN = admin-frame-prod-rehearsal-ca

[v3_ca]
basicConstraints = critical,CA:TRUE
keyUsage = critical,keyCertSign,cRLSign
EOF
openssl req -x509 -new -nodes -key "$OUT/ca.key" -sha256 -days 3650 \
  -config "$OUT/ca.cnf" -out "$OUT/ca.crt"

openssl genrsa -out "$OUT/server.key" 2048
write_req_cnf "$OUT/server.cnf" "config-api"
openssl req -new -key "$OUT/server.key" -config "$OUT/server.cnf" -out "$OUT/server.csr"
cat >"$OUT/server.ext" <<'EOF'
subjectAltName=DNS:localhost,DNS:config-api,IP:127.0.0.1
extendedKeyUsage=serverAuth
EOF
openssl x509 -req -in "$OUT/server.csr" -CA "$OUT/ca.crt" -CAkey "$OUT/ca.key" -CAcreateserial \
  -out "$OUT/server.crt" -days 825 -sha256 -extfile "$OUT/server.ext"

for role in "${ROLES[@]}"; do
  openssl genrsa -out "$OUT/client-${role}.key" 2048
  write_req_cnf "$OUT/client-${role}.cnf" "$role"
  openssl req -new -key "$OUT/client-${role}.key" -config "$OUT/client-${role}.cnf" -out "$OUT/client-${role}.csr"
  # 仅 CN=<role>：mTLS=require 时身份取 CN，与 CONFIG_SERVICE_IDENTITY / grant 白名单对齐。
  cat >"$OUT/client-${role}.ext" <<'EOF'
extendedKeyUsage=clientAuth
EOF
  openssl x509 -req -in "$OUT/client-${role}.csr" -CA "$OUT/ca.crt" -CAkey "$OUT/ca.key" -CAcreateserial \
    -out "$OUT/client-${role}.crt" -days 825 -sha256 -extfile "$OUT/client-${role}.ext"
  rm -f "$OUT/client-${role}.csr" "$OUT/client-${role}.ext" "$OUT/client-${role}.cnf"
done

# 兼容旧路径：默认 client 指向 config-api（健康检查 / 手工 curl）。
cp -f "$OUT/client-config-api.crt" "$OUT/client.crt"
cp -f "$OUT/client-config-api.key" "$OUT/client.key"

rm -f "$OUT/server.csr" "$OUT/server.ext" "$OUT/server.cnf" "$OUT/ca.cnf" "$OUT/ca.srl"
chmod 600 "$OUT"/*.key 2>/dev/null || true
printf 'OK: 证书已写入 %s（含各进程 client-*.crt）\n' "$OUT"
printf 'NOTE: 若服务已在运行，请 make prod-restart 使新证书生效。\n'

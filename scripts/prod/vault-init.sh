#!/usr/bin/env bash
# 本机 prod 演练：初始化 / 解封 file 存储 Vault，启用 Transit 并创建 configcenter 密钥。
#
# 前提：docker compose -f docker-compose.prod-rehearsal.yml up -d vault
# 用法：bash scripts/prod/vault-init.sh
#
# 首次 init 会把 unseal key / root token 写入 var/prod-vault/（gitignore）；
# 之后重启只需本脚本解封。prod-services 会优先读取 var/prod-vault/root.token。

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VAULT_ADDR="${VAULT_ADDR:-http://127.0.0.1:8200}"
KEY_NAME="${CONFIGCENTER_VAULT_TRANSIT_KEY:-configcenter}"
STATE_DIR="${PROD_VAULT_STATE_DIR:-$ROOT/var/prod-vault}"
INIT_JSON="$STATE_DIR/init.json"
ROOT_TOKEN_FILE="$STATE_DIR/root.token"
UNSEAL_KEY_FILE="$STATE_DIR/unseal.key"

mkdir -p "$STATE_DIR"

resolve_python() {
  if command -v python3 >/dev/null 2>&1; then
    command -v python3
  elif command -v python >/dev/null 2>&1; then
    command -v python
  else
    printf 'ERROR: 需要 python3 或 python（解析 seal-status JSON）\n' >&2
    exit 1
  fi
}

PY="$(resolve_python)"

printf '等待 Vault 进程就绪 (%s) ...\n' "$VAULT_ADDR"
for i in $(seq 1 60); do
  # 未初始化 / 已封印也算进程就绪（sys/health 用 uninitcode/sealedcode=200）。
  if curl -fsS -o /dev/null --max-time 2 \
    "$VAULT_ADDR/v1/sys/health?standbyok=true&uninitcode=200&sealedcode=200" 2>/dev/null; then
    break
  fi
  if [[ "$i" -eq 60 ]]; then
    printf 'ERROR: Vault 未就绪\n' >&2
    exit 1
  fi
  sleep 1
done

seal_status() {
  curl -fsS --max-time 5 "$VAULT_ADDR/v1/sys/seal-status"
}

initialized="$("$PY" -c "import sys,json; print(json.load(sys.stdin).get('initialized', False))" <<<"$(seal_status)")"
sealed="$("$PY" -c "import sys,json; print(json.load(sys.stdin).get('sealed', True))" <<<"$(seal_status)")"

if [[ "$initialized" != "True" ]]; then
  printf '首次初始化 Vault（1 share / threshold 1）...\n'
  curl -fsS --max-time 30 -X PUT \
    -H 'Content-Type: application/json' \
    -d '{"secret_shares":1,"secret_threshold":1}' \
    "$VAULT_ADDR/v1/sys/init" >"$INIT_JSON"
  "$PY" - "$INIT_JSON" "$ROOT_TOKEN_FILE" "$UNSEAL_KEY_FILE" <<'PY'
import json, sys
path, token_path, key_path = sys.argv[1:4]
data = json.load(open(path, encoding="utf-8"))
open(token_path, "w", encoding="utf-8").write(data["root_token"].strip() + "\n")
open(key_path, "w", encoding="utf-8").write(data["keys_base64"][0].strip() + "\n")
PY
  chmod 600 "$INIT_JSON" "$ROOT_TOKEN_FILE" "$UNSEAL_KEY_FILE" 2>/dev/null || true
  sealed=True
fi

if [[ ! -f "$ROOT_TOKEN_FILE" || ! -f "$UNSEAL_KEY_FILE" ]]; then
  printf 'ERROR: Vault 已初始化但缺少 %s / %s；请删除 Vault volume 后重跑，或恢复备份。\n' \
    "$ROOT_TOKEN_FILE" "$UNSEAL_KEY_FILE" >&2
  exit 1
fi

VAULT_TOKEN="$(tr -d '[:space:]' <"$ROOT_TOKEN_FILE")"
UNSEAL_KEY="$(tr -d '[:space:]' <"$UNSEAL_KEY_FILE")"
export VAULT_ADDR VAULT_TOKEN

if [[ "$sealed" == "True" ]]; then
  printf '解封 Vault ...\n'
  curl -fsS --max-time 15 -X PUT \
    -H 'Content-Type: application/json' \
    -d "{\"key\":\"${UNSEAL_KEY}\"}" \
    "$VAULT_ADDR/v1/sys/unseal" >/dev/null
fi

# unseal 后节点可能短暂非 active；等待 seal-status.sealed=false。
printf '等待 Vault 变为 active ...\n'
for i in $(seq 1 30); do
  sealed_now="$("$PY" -c "import sys,json; print(json.load(sys.stdin).get('sealed', True))" <<<"$(seal_status)")"
  if [[ "$sealed_now" == "False" ]]; then
    break
  fi
  if [[ "$i" -eq 30 ]]; then
    printf 'ERROR: Vault 解封后仍未 active\n' >&2
    exit 1
  fi
  sleep 1
done

# 幂等启用 transit；失败时重试（避免刚 active 时的瞬时错误被吞掉）。
mounted=0
for i in $(seq 1 10); do
  if curl -fsS -X POST -H "X-Vault-Token: $VAULT_TOKEN" \
    -H 'Content-Type: application/json' \
    -d '{"type":"transit"}' \
    "$VAULT_ADDR/v1/sys/mounts/transit" >/dev/null 2>&1; then
    mounted=1
    break
  fi
  # 已存在 mounts 时 GET 成功即视为就绪。
  if curl -fsS -H "X-Vault-Token: $VAULT_TOKEN" \
    "$VAULT_ADDR/v1/sys/mounts/transit" >/dev/null 2>&1; then
    mounted=1
    break
  fi
  sleep 1
done
if [[ "$mounted" -ne 1 ]]; then
  printf 'ERROR: 无法启用 transit secrets engine\n' >&2
  exit 1
fi

if ! curl -fsS -H "X-Vault-Token: $VAULT_TOKEN" \
  "$VAULT_ADDR/v1/transit/keys/${KEY_NAME}" >/dev/null 2>&1; then
  curl -fsS -X POST -H "X-Vault-Token: $VAULT_TOKEN" \
    -H 'Content-Type: application/json' \
    -d '{"type":"aes256-gcm96"}' \
    "$VAULT_ADDR/v1/transit/keys/${KEY_NAME}" >/dev/null
fi

curl -fsS -H "X-Vault-Token: $VAULT_TOKEN" \
  "$VAULT_ADDR/v1/transit/keys/${KEY_NAME}" >/dev/null

printf 'OK: Vault Transit key=%s addr=%s（token → %s）\n' \
  "$KEY_NAME" "$VAULT_ADDR" "$ROOT_TOKEN_FILE"
printf '请确认 scripts/prod/.env 含 CONFIGCENTER_KEK_DRIVER=vault；VAULT_TOKEN 由 prod-services 自动读取 root.token（仅注入 config-api）。\n'

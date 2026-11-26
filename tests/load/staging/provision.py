"""staging 压测账号池预置（STG-EQ-01 / STG-01 通用）。

A 端 `mfa.admin_required=true` 时，登录压测需要「每账号独立 TOTP」，本脚本负责：

1. `admin`      ：引导管理员首登改密 → 强制 MFA 绑定 → 落访问令牌，并签发 SCIM 令牌；
2. `accounts N` ：SCIM 建号（生产路径）→ 夹具注入 Argon2id 口令 → 逐账号绑定 TOTP；
3. `csv`        ：把账号池落成 `subject,totp` 文件，交给 `identity_login.js` 的
   `ACCOUNTS_FILE`（k6 进程内不可用环境变量传 2000 个密钥）。

口径说明：SCIM 账号按设计是纯 SSO 身份（无本地口令），因此口令由
`seedpw` 用生产同款编码器注入 `identity_credential`——账号、映射、MFA 仍全部走真实 API。

用法（仓库根目录，先启动 A 端并完成引导管理员创建）：
    python tests/load/staging/provision.py admin
    python tests/load/staging/provision.py accounts 2000
    python tests/load/staging/provision.py csv

环境变量：BASE_URL（默认 http://127.0.0.1:8080）、MYSQL_CONTAINER（默认
admin-frame-integration-mysql-1）、MYSQL_DB（默认 admin_frame_stg）、DRILL_STATE
（默认 tmp/stg-eq/state.json，存放令牌与 TOTP 密钥，勿入库）。
"""

import base64
import hashlib
import hmac
import json
import os
import struct
import subprocess
import sys
import time
import uuid
from concurrent.futures import ThreadPoolExecutor, as_completed

import requests

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))
BASE = os.environ.get("BASE_URL", "http://127.0.0.1:8080")
STATE = os.environ.get("DRILL_STATE", os.path.join(REPO_ROOT, "tmp", "stg-eq", "state.json"))
MYSQL_CONTAINER = os.environ.get("MYSQL_CONTAINER", "admin-frame-integration-mysql-1")
MYSQL_DB = os.environ.get("MYSQL_DB", "admin_frame_stg")
ACCOUNTS_CSV = os.path.join(os.path.dirname(STATE), "accounts.csv")
ADMIN_SUBJECT = "admin@example.com"
ADMIN_BOOTSTRAP_PASSWORD = "Admin#Bootstrap2026"
ADMIN_PASSWORD = "Admin#Staging2026"
LOAD_PASSWORD = "Load#Staging2026"

_s = requests.Session()
_s.headers.update({"Content-Type": "application/json"})


def totp(secret, digits=6, period=30, at=None):
    key = base64.b32decode(secret.upper() + "=" * ((8 - len(secret) % 8) % 8))
    counter = int((at or time.time()) // period)
    digest = hmac.new(key, struct.pack(">Q", counter), hashlib.sha1).digest()
    offset = digest[-1] & 0x0F
    code = struct.unpack(">I", digest[offset:offset + 4])[0] & 0x7FFFFFFF
    return str(code % (10 ** digits)).zfill(digits)


def post(path, body=None, token=None, expect=(200, 204), idempotent=True):
    headers = {}
    if token:
        headers["Authorization"] = "Bearer " + token
    if idempotent:
        # A 端特权写端点强制携带幂等键（identity 路由级中间件）。
        headers["Idempotency-Key"] = uuid.uuid4().hex
    resp = _s.post(BASE + path, data=json.dumps(body or {}), headers=headers, timeout=30)
    try:
        payload = resp.json()
    except ValueError:
        payload = {}
    if resp.status_code not in expect:
        raise RuntimeError(f"{path} -> {resp.status_code} {payload or resp.text[:200]}")
    return payload.get("data", payload)


def get(path, token=None):
    headers = {"Authorization": "Bearer " + token} if token else {}
    resp = _s.get(BASE + path, headers=headers, timeout=30)
    payload = resp.json()
    if resp.status_code != 200:
        raise RuntimeError(f"{path} -> {resp.status_code} {payload}")
    return payload.get("data", payload)


def save_state(data):
    os.makedirs(os.path.dirname(STATE), exist_ok=True)
    current = {}
    if os.path.exists(STATE):
        with open(STATE, encoding="utf-8") as fh:
            current = json.load(fh)
    current.update(data)
    with open(STATE, "w", encoding="utf-8") as fh:
        json.dump(current, fh, ensure_ascii=False, indent=2)


def load_state():
    with open(STATE, encoding="utf-8") as fh:
        return json.load(fh)


def admin_login():
    """引导管理员：首登改密 → 强制 MFA 绑定 → 令牌。"""
    try:
        data = post("/admin/v1/auth/login", {"subject": ADMIN_SUBJECT, "password": ADMIN_BOOTSTRAP_PASSWORD})
    except RuntimeError:
        # 已经完成首登改密：直接用演练口令 + 已绑定 TOTP 登录。
        data = post("/admin/v1/auth/login", {"subject": ADMIN_SUBJECT, "password": ADMIN_PASSWORD})
        if data.get("mfa_required"):
            data = post("/admin/v1/auth/mfa/verify",
                        {"mfa_ticket": data["mfa_ticket"], "code": admin_mfa_code()})
    if data.get("password_change_required"):
        data = post("/admin/v1/auth/password/change",
                    {"password_change_ticket": data["password_change_ticket"], "new_password": ADMIN_PASSWORD})
    if data.get("mfa_binding_required"):
        ticket = data["mfa_setup_ticket"]
        setup = post("/admin/v1/auth/mfa/setup", {"mfa_setup_ticket": ticket})
        data = post("/admin/v1/auth/mfa/verify", {"mfa_setup_ticket": ticket, "code": totp(setup["secret"])})
        save_state({"admin_totp": setup["secret"]})
    if "access_token" not in data:
        raise RuntimeError(f"admin login did not yield tokens: {list(data)}")
    save_state({"admin_access_token": data["access_token"]})
    return data["access_token"]


def admin_token():
    if os.path.exists(STATE):
        token = load_state().get("admin_access_token", "")
        if token:
            try:
                get("/admin/v1/me", token)
                return token
            except RuntimeError:
                pass
    return admin_login()


def admin_mfa_code():
    state = load_state()
    if "admin_totp" not in state:
        admin_login()
        state = load_state()
    return totp(state["admin_totp"])


def scim_token(admin):
    """签发 SCIM Bearer（库内只存 SHA-256）。"""
    if os.path.exists(STATE) and load_state().get("scim_token"):
        return load_state()["scim_token"]
    data = post("/admin/v1/auth/scim/tokens", {"name": "stg-eq-drill"}, token=admin)
    save_state({"scim_token": data["token"], "scim_token_id": data.get("id")})
    return data["token"]


def scim_create_user(token, index):
    subject = f"load-{index:04d}"
    body = {
        "schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"],
        "externalId": subject,
        "userName": f"{subject}@load.example.com",
        "displayName": f"Load {index:04d}",
        "active": True,
    }
    try:
        return post("/admin/scim/v2/Users", body, token=token, expect=(201,))
    except RuntimeError as exc:
        if "409" not in str(exc):
            raise
        # 幂等复跑：按 externalId 取回已存在资源。
        found = get(f'/admin/scim/v2/Users?filter=externalId eq "{subject}"', token)
        resources = found.get("Resources") or []
        if not resources:
            raise
        return resources[0]


def account_ids(admin, prefix="load-"):
    ids, page = [], 1
    while True:
        data = get(f"/admin/v1/auth/accounts?page={page}&page_size=100&keyword={prefix}", admin)
        items = data.get("items") or []
        ids.extend((item["id"], item.get("subject") or item.get("user_name")) for item in items)
        if len(items) < 100:
            return ids
        page += 1


def seed_credentials(account_ids_):
    """夹具注入口令凭据：SCIM 账号按设计无本地口令（纯 SSO），登录 SLO 需覆盖口令链路。"""
    sql_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "creds.sql")
    ids_file = os.path.join(os.path.dirname(STATE), "account-ids.txt")
    with open(ids_file, "w", encoding="utf-8") as fh:
        fh.write("\n".join(account_ids_))
    gen = subprocess.run(
        ["go", "run", "./tests/load/staging/seedpw", "-password", LOAD_PASSWORD, "-ids-file", ids_file],
        cwd=REPO_ROOT,
        capture_output=True, text=True, check=True)
    with open(sql_path, "w", encoding="utf-8") as fh:
        fh.write(gen.stdout)
    subprocess.run(["docker", "exec", "-i", MYSQL_CONTAINER,
                    "mysql", "-uapp", "-papp", MYSQL_DB],
                   stdin=open(sql_path, encoding="utf-8"), check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    return len(account_ids_)


def bind_account(account_id, subject):
    """首登 → 强制 MFA 绑定（口令由夹具注入，账号无改密标记）。返回 (subject, secret)。"""
    data = post("/admin/v1/auth/login", {"subject": subject, "password": LOAD_PASSWORD})
    if data.get("password_change_required"):
        raise RuntimeError(f"{subject}: unexpected password_change_required")
    if not data.get("mfa_binding_required"):
        raise RuntimeError(f"{subject}: expected mfa_binding_required, got {list(data)}")
    ticket = data["mfa_setup_ticket"]
    setup = post("/admin/v1/auth/mfa/setup", {"mfa_setup_ticket": ticket})
    post("/admin/v1/auth/mfa/verify", {"mfa_setup_ticket": ticket, "code": totp(setup["secret"])})
    return subject, setup["secret"]


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else "admin"
    if mode == "admin":
        token = admin_token()
        scim = scim_token(token)
        print("admin ok; scim token persisted")
        print("scim_token_prefix", scim[:12])
        return
    if mode == "accounts":
        total = int(sys.argv[2]) if len(sys.argv) > 2 else 2000
        admin = admin_token()
        scim = scim_token(admin)
        started = time.time()
        created, errors = [], []
        with ThreadPoolExecutor(max_workers=16) as pool:
            futures = [pool.submit(scim_create_user, scim, i) for i in range(1, total + 1)]
            for fut in as_completed(futures):
                try:
                    created.append(fut.result())
                except Exception as exc:  # noqa: BLE001 - 演练脚本，收集后统一报告
                    errors.append(str(exc))
        print(f"scim users: {total} requested, {len(created)} ok, {len(errors)} errors, {time.time() - started:.1f}s")
        if errors:
            print(errors[:3])
            if not created:
                raise SystemExit(1)

        ids = [(item["id"], item.get("userName")) for item in created]
        seeded = seed_credentials([aid for aid, _ in ids])
        print(f"credentials seeded: {seeded}")

        started = time.time()
        secrets, errors = [], []
        with ThreadPoolExecutor(max_workers=12) as pool:
            futures = {pool.submit(bind_account, aid, subject): subject for aid, subject in ids}
            done = 0
            for fut in as_completed(futures):
                done += 1
                try:
                    secrets.append(fut.result())
                except Exception as exc:  # noqa: BLE001
                    errors.append(f"{futures[fut]}: {exc}")
                if done % 250 == 0:
                    print(f"  bound {done}/{len(ids)} ({time.time() - started:.0f}s)")
        print(f"bound {len(secrets)} accounts, errors={len(errors)}, {time.time() - started:.1f}s")
        if errors:
            print(errors[:3])
        save_state({"load_accounts": [{"subject": s, "totp": t} for s, t in secrets],
                    "load_password": LOAD_PASSWORD})
        return
    if mode == "csv":
        state = load_state()
        rows = state.get("load_accounts") or []
        os.makedirs(os.path.dirname(ACCOUNTS_CSV), exist_ok=True)
        with open(ACCOUNTS_CSV, "w", encoding="utf-8") as fh:
            for item in rows:
                fh.write(f"{item['subject']},{item['totp']}\n")
        print(f"accounts file: {ACCOUNTS_CSV} ({len(rows)} entries)")
        print(f"password: {state.get('load_password', LOAD_PASSWORD)}")
        return
    raise SystemExit(f"unknown mode {mode}")


if __name__ == "__main__":
    main()

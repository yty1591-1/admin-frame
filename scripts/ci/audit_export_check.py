#!/usr/bin/env python3
"""AUD/ENT-04 T5 一致性检查：路由 ↔ OpenAPI ↔ 状态枚举。

三处任一漂移都会让「契约里写了、代码里没有」或反向发生，因此放在门禁里锁死：

1. `internal/audit/transport/admin/router.go` 注册的审计路由，必须在
   `api/openapi/admin.yaml` 里有对应路径；
2. `api/openapi/paths/admin/audit.yaml` 的异步导出路径必须被 admin.yaml 引用；
3. `internal/audit/domain/export.go` 的状态常量必须与
   `api/openapi/schemas/admin/audit.yaml` 里 `status.enum` 完全一致。

用法：python3 scripts/ci/audit_export_check.py
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

ROUTER = ROOT / "internal/audit/transport/admin/router.go"
DOMAIN = ROOT / "internal/audit/domain/export.go"
ADMIN_SPEC = ROOT / "api/openapi/admin.yaml"
PATHS_SPEC = ROOT / "api/openapi/paths/admin/audit.yaml"
SCHEMAS_SPEC = ROOT / "api/openapi/schemas/admin/audit.yaml"

# 路由 → OpenAPI 路径。新增审计路由必须同步这里与 spec，否则门禁失败。
ROUTE_TO_PATH = {
    "GET /admin/v1/audit/events": "/admin/v1/audit/events",
    "GET /admin/v1/audit/events/export": "/admin/v1/audit/events/export",
    "POST /admin/v1/audit/events/export": "/admin/v1/audit/events/export",
    "GET /admin/v1/audit/exports/{id}": "/admin/v1/audit/exports/{id}",
    "GET /admin/v1/audit/exports/{id}/download": "/admin/v1/audit/exports/{id}/download",
}


def read(path: Path) -> str:
    if not path.is_file():
        raise SystemExit(f"缺少文件：{path.relative_to(ROOT)}")
    return path.read_text(encoding="utf-8")


def registered_routes() -> set[str]:
    """从 mux.Handle("METHOD /path", ...) 提取已注册路由。"""
    text = read(ROUTER)
    return set(re.findall(r'mux\.Handle\("([A-Z]+ [^"]+)"', text))


def spec_paths() -> set[str]:
    text = read(ADMIN_SPEC)
    return set(re.findall(r"^  (/[^\s:]+):\s*$", text, flags=re.MULTILINE))


def domain_statuses() -> list[str]:
    text = read(DOMAIN)
    return re.findall(r'ExportStatus = "([a-z]+)"', text)


def spec_statuses() -> list[str]:
    """取 AuditExportJob.status 的 enum（文件里有多个 enum，必须按块定位）。"""
    text = read(SCHEMAS_SPEC)
    block = re.search(r"^\s{4}AuditExportJob:\s*$", text, flags=re.MULTILINE)
    if not block:
        raise SystemExit("schemas/admin/audit.yaml 缺少 AuditExportJob 定义")
    tail = text[block.end():]
    next_block = re.search(r"^\s{4}[A-Za-z]+:\s*$", tail, flags=re.MULTILINE)
    scope = tail[: next_block.start()] if next_block else tail
    status = re.search(r"^\s+status:\s*$", scope, flags=re.MULTILINE)
    if not status:
        raise SystemExit("AuditExportJob 缺少 status 字段")
    enum = re.search(r"^\s+enum:\s*\[([^\]]+)\]", scope[status.end():], flags=re.MULTILINE)
    if not enum:
        raise SystemExit("AuditExportJob.status 缺少 enum")
    return [item.strip() for item in enum.group(1).split(",")]


def main() -> int:
    failures: list[str] = []

    routes = registered_routes()
    paths = spec_paths()
    for route, spec_path in ROUTE_TO_PATH.items():
        if route not in routes:
            failures.append(f"router.go 未注册路由：{route}")
        if spec_path not in paths:
            failures.append(f"openapi/admin.yaml 缺少路径：{spec_path}（路由 {route}）")

    extra = routes - set(ROUTE_TO_PATH)
    for route in sorted(extra):
        failures.append(f"router.go 新增了未纳管的路由：{route}（补 ROUTE_TO_PATH 与 spec）")

    # 异步导出路径必须真的被引用，否则文件存在但 spec 里不可见。
    admin_spec = read(ADMIN_SPEC)
    paths_spec = read(PATHS_SPEC)
    for path_key in (
        "admin-v1-audit-events-export",
        "admin-v1-audit-exports-by-id",
        "admin-v1-audit-exports-by-id-download",
    ):
        if f"paths/admin/audit.yaml#/{path_key}" not in admin_spec:
            failures.append(f"admin.yaml 未引用 paths/admin/audit.yaml#/{path_key}")
        if f"\n{path_key}:" not in paths_spec:
            failures.append(f"paths/admin/audit.yaml 缺少 {path_key}")

    # 状态枚举：代码是唯一事实源，spec 必须逐个对齐（多一个少一个都算漂移）。
    code_statuses = domain_statuses()
    if not code_statuses:
        failures.append("domain/export.go 未解析到 ExportStatus 常量")
    declared = spec_statuses()
    if sorted(code_statuses) != sorted(declared):
        failures.append(
            "状态枚举不一致：domain=%s, openapi=%s" % (sorted(code_statuses), sorted(declared))
        )

    if failures:
        for item in failures:
            print(f"ERROR: {item}", file=sys.stderr)
        return 1
    print(f"    OK: {len(ROUTE_TO_PATH)} 条审计路由与 OpenAPI 一致；状态枚举 {len(code_statuses)} 项对齐")
    return 0


if __name__ == "__main__":
    sys.exit(main())

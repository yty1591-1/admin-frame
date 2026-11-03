#!/usr/bin/env python3
"""AUD-06 追加写检查：业务代码不得 UPDATE / DELETE 审计事件表。

审计表只允许保留期任务删除（`SQLRetentionStore`），且必须是「先归档、后按 ID 删除」。
这条规则此前只写在标准里靠 Review，本脚本把它变成可执行的约束：

1. 全仓库非测试 Go 代码中，不得出现对 `audit_event` 的 UPDATE，也不得出现
   字面量 `DELETE FROM audit_event`（保留期走 fmt 拼表名，另见第 2 条）；
2. `internal/audit` 下的 `DELETE FROM` 只允许出现在 `sql_reader.go`
   （`SQLRetentionStore.DeleteBefore/DeleteIDs` 所在文件）；
3. `internal/audit` 下不得出现 `UPDATE` 语句（审计事件不可变更）。

用法：python3 scripts/ci/audit_immutability_check.py
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

# 允许出现删除审计行的唯一实现文件（保留期任务）。
ALLOWED_DELETE_FILE = Path("internal/audit/infrastructure/persistence/sql_reader.go")

DELETE_PATTERN = re.compile(r"DELETE\s+FROM", re.IGNORECASE)
UPDATE_AUDIT_PATTERN = re.compile(r"UPDATE\s+audit_event", re.IGNORECASE)
DELETE_AUDIT_PATTERN = re.compile(r"DELETE\s+FROM\s+audit_event", re.IGNORECASE)


def go_files(*roots: str) -> list[Path]:
    files: list[Path] = []
    for root in roots:
        base = ROOT / root
        if not base.is_dir():
            continue
        for path in base.rglob("*.go"):
            if path.name.endswith("_test.go"):
                continue
            files.append(path)
    return files


def main() -> int:
    failures: list[str] = []

    for path in go_files("internal", "cmd", "pkg"):
        rel = path.relative_to(ROOT)
        text = path.read_text(encoding="utf-8", errors="ignore")
        for match in UPDATE_AUDIT_PATTERN.finditer(text):
            line = text[: match.start()].count("\n") + 1
            failures.append(f"{rel}:{line} 出现 UPDATE audit_event（AUD-06 禁止变更审计行）")
        for match in DELETE_AUDIT_PATTERN.finditer(text):
            line = text[: match.start()].count("\n") + 1
            failures.append(f"{rel}:{line} 出现 DELETE FROM audit_event（只允许保留期按 ID 删除）")

    for path in go_files("internal/audit"):
        rel = path.relative_to(ROOT)
        text = path.read_text(encoding="utf-8", errors="ignore")
        if rel != ALLOWED_DELETE_FILE:
            for match in DELETE_PATTERN.finditer(text):
                line = text[: match.start()].count("\n") + 1
                failures.append(
                    f"{rel}:{line} 在保留期仓储之外出现 DELETE FROM（AUD-06）"
                )
        # 只有涉及审计事件表的文件才禁止 UPDATE；导出任务表（audit_export_job）
        # 本身是需要状态流转的可变表，不受 AUD-06 约束。
        if "audit_event" in text:
            for match in re.finditer(r"\bUPDATE\b", text):
                line = text[: match.start()].count("\n") + 1
                failures.append(
                    f"{rel}:{line} 出现 UPDATE 且同文件涉及 audit_event（审计事件不可变更，AUD-06）"
                )

    # 保留期仓储本身必须仍是「按 cutoff + 按 ID」两种受控删除，不能出现无条件删除。
    allowed = ROOT / ALLOWED_DELETE_FILE
    if allowed.is_file():
        text = allowed.read_text(encoding="utf-8", errors="ignore")
        if re.search(r"DELETE\s+FROM\s+\S+\s*`", text):
            failures.append(f"{ALLOWED_DELETE_FILE} 出现无 WHERE 的删除语句")

    if failures:
        for item in failures:
            print(f"ERROR: {item}", file=sys.stderr)
        return 1
    print("    OK: 审计事件表仅保留期任务可删，且无 UPDATE 路径")
    return 0


if __name__ == "__main__":
    sys.exit(main())

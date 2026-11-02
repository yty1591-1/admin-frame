#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""校验 docs/ 与 README.md 中的相对 Markdown 链接是否指向真实文件。

只检查以 ./ 或 ../ 开头的相对链接；http(s)、mailto、纯锚点跳过。
用法：
    python3 scripts/ci/doclink_check.py            # 检查全库
    python3 scripts/ci/doclink_check.py docs/adr   # 只检查指定目录
"""

import pathlib
import re
import sys

REPO_ROOT = pathlib.Path(__file__).resolve().parent.parent.parent

# 用字符类与显式字符拼接，避免依赖反斜杠转义。
LINK = re.compile(r"\]\((\./|\.\./)([^)#]+)(#[^)]*)?\)")


def collect(targets):
    files = []
    for t in targets:
        p = (REPO_ROOT / t).resolve()
        if p.is_dir():
            files.extend(sorted(p.rglob("*.md")))
        elif p.is_file():
            files.append(p)
    return files


def check(files):
    broken = []
    count = 0
    for f in files:
        text = f.read_text(encoding="utf-8")
        for lineno, line in enumerate(text.splitlines(), 1):
            for m in LINK.finditer(line):
                rel = m.group(1) + m.group(2)
                count += 1
                if not (f.parent / rel).resolve().exists():
                    broken.append((f, lineno, rel))
    return count, broken


def main(argv):
    targets = argv[1:] or ["docs", "README.md"]
    files = collect(targets)
    if not files:
        print("ERROR: 未找到待检查的 Markdown 文件", file=sys.stderr)
        return 1

    count, broken = check(files)
    print(f"==> 文档链接检查：{len(files)} 个文件，{count} 条相对链接")
    if not broken:
        print("OK: 所有相对链接均有效")
        return 0

    for f, lineno, rel in broken:
        try:
            shown = f.relative_to(REPO_ROOT)
        except ValueError:
            shown = f
        print(f"ERROR: {shown}:{lineno} -> {rel}（目标不存在）", file=sys.stderr)
    print(f"文档链接检查失败：{len(broken)} 条失效链接", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))

#!/usr/bin/env python3
"""校验 Grafana 看板的每个面板查询在真实 Prometheus 上能出数。

背景（ENT-06 AC-ENT-06-3）：看板 JSON 入库后从未在真集群验证过，
面板里的 PromQL 可能引用了已改名的指标或写错的标签，结果是「看板打开是空的」。

用法：
    python3 scripts/observability/check-dashboards.py \
        --dashboards deploy/observability/grafana/dashboards

    # 走 kubectl exec 访问集群内的 Prometheus（默认）：
    python3 scripts/observability/check-dashboards.py

    # 直连 Prometheus（本地已做端口转发）：
    python3 scripts/observability/check-dashboards.py --prom-url http://127.0.0.1:9090

退出码：
    0  全部面板查询有数据
    1  存在查询报错（PromQL 非法 / 指标不存在）
    2  存在查询合法但无数据（看板面板会是空的）
"""

from __future__ import annotations

import argparse
import json
import pathlib
import subprocess
import sys
import urllib.parse
import urllib.request


def collect_queries(node, out):
    """递归收集面板与行（含 row 内嵌套面板）的查询表达式。

    node 可以是看板（dict，含 panels）或面板列表（list）。
    面板带 "expect-empty" 标签时，其查询被记为「健康态本就无数据」，不计为失败。
    """
    if isinstance(node, list):
        for child in node:
            collect_queries(child, out)
        return
    if not isinstance(node, dict):
        return
    title = node.get("title", "")
    expect_empty = "expect-empty" in (node.get("tags") or [])
    for target in node.get("targets", []) or []:
        expr = target.get("expr")
        if isinstance(expr, str) and expr.strip():
            out.append((title or "(无标题)", expr, expect_empty))
    collect_queries(node.get("panels", []) or [], out)


def query_prometheus(expr: str, prom_url: str | None, namespace: str, deploy: str) -> tuple[str, str]:
    """返回 (status, detail)：status ∈ {ok, empty, error}。"""
    url = f"{prom_url.rstrip('/')}/api/v1/query?query={urllib.parse.quote(expr)}" if prom_url else None
    if url:
        try:
            with urllib.request.urlopen(url, timeout=30) as resp:
                payload = json.loads(resp.read().decode("utf-8"))
        except Exception as exc:  # noqa: BLE001 - 任何取数失败都按查询错误上报
            return "error", f"请求失败：{exc}"
    else:
        cmd = [
            "kubectl", "-n", namespace, "exec", f"deploy/{deploy}", "--",
            "wget", "-qO-", f"http://127.0.0.1:9090/api/v1/query?query={urllib.parse.quote(expr)}",
        ]
        proc = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
        if proc.returncode != 0 or not proc.stdout.strip():
            return "error", (proc.stderr or "kubectl exec 失败").strip()[:200]
        try:
            payload = json.loads(proc.stdout)
        except json.JSONDecodeError as exc:
            return "error", f"响应不是 JSON：{exc}"

    if payload.get("status") != "success":
        return "error", str(payload.get("error", "未知错误"))[:200]
    data = payload.get("data", {})
    if data.get("resultType") == "scalar":
        return ("ok", "标量有值") if float(data.get("result", [0, "0"])[1]) else ("empty", "标量为 0")
    results = data.get("result", [])
    if not results:
        return "empty", "无数据点"
    return "ok", f"{len(results)} 个序列"


def main() -> int:
    parser = argparse.ArgumentParser(description="校验 Grafana 看板面板查询")
    parser.add_argument("--dashboards", default="deploy/observability/grafana/dashboards")
    parser.add_argument("--prom-url", default=None, help="直连 Prometheus 地址；缺省走 kubectl exec")
    parser.add_argument("--prom-namespace", default="monitoring")
    parser.add_argument("--prom-deploy", default="prometheus-standalone")
    parser.add_argument("--allow-empty", action="store_true", help="允许查询无数据（不判失败）")
    args = parser.parse_args()

    dashboard_dir = pathlib.Path(args.dashboards)
    if not dashboard_dir.is_dir():
        print(f"ERROR: 看板目录不存在：{dashboard_dir}", file=sys.stderr)
        return 1

    total = 0
    errors: list[tuple[str, str, str]] = []
    empties: list[tuple[str, str, str]] = []
    expected_empties: list[tuple[str, str, str]] = []
    for path in sorted(dashboard_dir.glob("*.json")):
        try:
            doc = json.loads(path.read_text(encoding="utf-8"))
        except json.JSONDecodeError as exc:
            errors.append((path.name, "(解析)", f"JSON 非法：{exc}"))
            continue
        queries: list[tuple[str, str, bool]] = []
        collect_queries(doc.get("panels", []), queries)
        if not queries:
            print(f"[SKIP] {path.name}: 没有面板查询")
            continue
        print(f"[CHECK] {path.name} ({len(queries)} 个查询)")
        for panel, expr, expect_empty in queries:
            total += 1
            status, detail = query_prometheus(expr, args.prom_url, args.prom_namespace, args.prom_deploy)
            short = " ".join(expr.split())[:96]
            print(f"   [{status:5}] {panel} :: {short}  ({detail})")
            if status == "error":
                errors.append((path.name, panel, detail))
            elif status == "empty":
                if expect_empty:
                    expected_empties.append((path.name, panel, detail))
                else:
                    empties.append((path.name, panel, detail))

    print()
    print(
        f"汇总：共 {total} 个面板查询，错误 {len(errors)}，空数据 {len(empties)}，"
        f"预期空（expect-empty 标签）{len(expected_empties)}"
    )
    for name, panel, detail in errors:
        print(f"  ERROR   {name} / {panel}: {detail}")
    for name, panel, detail in empties:
        print(f"  EMPTY   {name} / {panel}: {detail}")
    for name, panel, detail in expected_empties:
        print(f"  OK-EMPTY {name} / {panel}: {detail}（面板声明健康态即无数据）")
    if errors:
        return 1
    if empties and not args.allow_empty:
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

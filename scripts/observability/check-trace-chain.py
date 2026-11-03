#!/usr/bin/env python3
"""校验「一次请求的 trace 可从入口追到 DB」（ENT-06 AC-ENT-06-4）。

判定条件（同一条 trace 内同时满足才算通过）：
  1. 存在入口 span：span.kind=server，且 operationName / http.route 含指定路由；
  2. 存在 DB span：operationName 以 sql. 开头、带 db.system 标签、span.kind=client；
  3. 两者在同一条父子链上：从 DB span 沿 references 上溯可到达入口 span。

用法：
  python3 scripts/observability/check-trace-chain.py \
      --service admin-api --route /admin/v1/audit/events

  # 默认从 monitoring/jaeger 里 kubectl exec 取数，可改：
  python3 scripts/observability/check-trace-chain.py --jaeger-ns monitoring --jaeger-deploy jaeger
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from typing import Any
from urllib.parse import quote


def fetch_traces(args: argparse.Namespace, operation: str | None) -> dict[str, Any]:
    url = (
        f"http://127.0.0.1:16686/api/traces?service={args.service}"
        f"&limit={args.limit}&lookback={args.lookback}"
    )
    if operation:
        url += f"&operation={quote(operation, safe='')}"
    cmd = ["kubectl", "-n", args.jaeger_ns, "exec", f"deploy/{args.jaeger_deploy}", "--", "wget", "-qO-", url]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise SystemExit(f"查询 Jaeger 失败：{' '.join(cmd)}\n{result.stderr.strip()}")
    if not result.stdout.strip():
        return {"data": []}
    return json.loads(result.stdout)


def tag_value(span: dict[str, Any], key: str) -> str:
    for tag in span.get("tags", []) or []:
        if tag.get("key") == key:
            return str(tag.get("value"))
    return ""


def parent_ids(span: dict[str, Any]) -> list[str]:
    return [ref.get("spanID", "") for ref in span.get("references", []) or [] if ref.get("spanID")]


def chain_to_root(span_id: str, by_id: dict[str, dict[str, Any]]) -> list[dict[str, Any]]:
    """从 span 上溯到 root，返回 [span, parent, ..., root]；有环则截断。"""
    chain: list[dict[str, Any]] = []
    seen: set[str] = set()
    current = by_id.get(span_id)
    while current is not None and current["spanID"] not in seen:
        chain.append(current)
        seen.add(current["spanID"])
        parents = parent_ids(current)
        current = by_id.get(parents[0]) if parents else None
    return chain


def main() -> int:
    parser = argparse.ArgumentParser(description="校验 trace 链路是否从入口贯通到 DB")
    parser.add_argument("--service", default="admin-api", help="Jaeger 中的服务名")
    parser.add_argument("--route", default="/admin/v1/audit/events", help="入口路由（子串匹配）")
    parser.add_argument("--jaeger-ns", default="monitoring")
    parser.add_argument("--jaeger-deploy", default="jaeger")
    parser.add_argument("--limit", type=int, default=50)
    parser.add_argument("--lookback", default="2h")
    parser.add_argument(
        "--operation",
        default=None,
        help="入口 span 的操作名（默认用 GET <route>）；按操作名检索可避开"
             "指标采集产生的纯 SQL 根 span 干扰。",
    )
    args = parser.parse_args()

    operation = args.operation
    if operation is None and args.route:
        operation = f"GET {args.route}"
    payload = fetch_traces(args, operation)
    traces = payload.get("data") or []
    if not traces and operation:
        print(f"提示：按 operation={operation!r} 无结果，退化为按 service 检索", file=sys.stderr)
        payload = fetch_traces(args, None)
        traces = payload.get("data") or []
    if not traces:
        print(f"FAIL: Jaeger 中没有 service={args.service} 的 trace（先跑一轮请求）", file=sys.stderr)
        return 1

    print(f"检查 {len(traces)} 条 trace（service={args.service}，route≈{args.route}）")
    for trace in traces:
        spans = trace.get("spans") or []
        by_id = {span["spanID"]: span for span in spans}

        server_spans = [
            span for span in spans
            if tag_value(span, "span.kind") == "server"
            and (args.route in span.get("operationName", "") or args.route in tag_value(span, "http.route"))
        ]
        db_spans = [
            span for span in spans
            if span.get("operationName", "").startswith("sql.")
            and tag_value(span, "db.system")
            and tag_value(span, "span.kind") == "client"
        ]
        # 优先拿带语句的 span 作为证据：db.statement/db.query.text 能直接说明打到了哪张表。
        db_spans.sort(key=lambda span: 0 if (tag_value(span, "db.statement") or tag_value(span, "db.query.text")) else 1)
        if not server_spans or not db_spans:
            continue

        for db_span in db_spans:
            chain = chain_to_root(db_span["spanID"], by_id)
            for server_span in server_spans:
                if any(item["spanID"] == server_span["spanID"] for item in chain):
                    trace_id = trace.get("traceID", "?")
                    print(f"PASS: trace {trace_id} 入口 → DB 链路成立")
                    for depth, item in enumerate(reversed(chain)):
                        indent = "  " * depth
                        stmt = tag_value(item, "db.statement") or tag_value(item, "db.query.text")
                        extra = f"  [{tag_value(item, 'db.system')}]"
                        if stmt:
                            extra += f"  {stmt[:80]}"
                        print(f"{indent}- {item.get('operationName')} "
                              f"({item.get('duration', 0) / 1000:.1f}ms){extra}")
                    return 0

    kinds = sorted({tag_value(span, "span.kind") for trace in traces for span in trace.get("spans") or []})
    ops = sorted({span.get("operationName", "") for trace in traces for span in trace.get("spans") or []})
    print("FAIL: 未找到「入口 span → SQL span」同链的 trace", file=sys.stderr)
    print(f"  出现过的 span.kind: {kinds}", file=sys.stderr)
    print(f"  出现过的 operation（前 20）: {ops[:20]}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())

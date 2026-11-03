#!/usr/bin/env python3
"""校验 Grafana 看板引用的指标名在代码或规则文件里有定义。

背景（ENT-06 T6）：看板 JSON 入库后从未校验过与代码的一致性。
指标一旦改名，看板不会报错，只会「打开是空的」——运维看到的是空面板，
而不是"这个指标不存在"。本门禁把这类漂移挡在 CI。

判定：看板里出现的形如 xxx_yyy 的标识符（排除 PromQL 函数与标签名），
必须在 Go 源码（指标名的字符串字面量）或告警规则 YAML 里有定义。

用法：python3 scripts/ci/dashboard_metrics_check.py
退出码：0 通过；1 存在未定义的指标名。
"""

from __future__ import annotations

import json
import pathlib
import re
import sys

# PromQL 函数与关键字：不是指标名。
PROMQL_KEYWORDS = {
    "sum", "rate", "increase", "avg", "min", "max", "count", "histogram_quantile",
    "quantile", "clamp_min", "clamp_max", "by", "without", "on", "ignoring",
    "group_left", "group_right", "or", "and", "unless", "offset", "bool",
    "le", "quantile_over_time", "absent", "absent_over_time", "vector",
    "label_replace", "label_join", "round", "ceil", "floor", "delta", "idelta",
    "deriv", "predict_linear", "holt_winters", "irate", "changes", "resets",
}

# 标签名：出现在 by (...) 聚合里，不是指标名。
LABEL_NAMES = {
    "service", "result", "layer", "stage", "reason", "channel", "op", "provider",
    "kind", "outcome", "static_allow", "iam_allow", "status", "code", "route",
    "method", "env", "instance", "job", "pod", "le", "quantile", "action",
    "actor", "subject", "resource", "scope", "type", "source", "target",
}

METRIC_RE = re.compile(r"\b([a-z][a-z0-9]*_[a-z0-9_]+)\b")


def collect_metrics(node, out):
    if isinstance(node, list):
        for child in node:
            collect_metrics(child, out)
        return
    if not isinstance(node, dict):
        return
    for target in node.get("targets", []) or []:
        expr = target.get("expr")
        if isinstance(expr, str):
            out.append(expr)
    collect_metrics(node.get("panels", []) or [], out)


def main() -> int:
    root = pathlib.Path(__file__).resolve().parents[2]
    dashboards = root / "deploy/observability/grafana/dashboards"
    sources = [root / "pkg", root / "internal", root / "cmd", root / "deploy/observability/prometheus/rules"]

    known: set[str] = set()
    for source in sources:
        if not source.exists():
            continue
        for path in source.rglob("*"):
            if path.is_file() and path.suffix in {".go", ".yaml", ".yml"}:
                try:
                    text = path.read_text(encoding="utf-8", errors="ignore")
                except OSError:
                    continue
                known.update(METRIC_RE.findall(text))

    used: dict[str, set[str]] = {}
    for path in sorted(dashboards.glob("*.json")):
        doc = json.loads(path.read_text(encoding="utf-8"))
        exprs: list[str] = []
        collect_metrics(doc.get("panels", []), exprs)
        for expr in exprs:
            for name in METRIC_RE.findall(expr):
                if name in PROMQL_KEYWORDS or name in LABEL_NAMES:
                    continue
                used.setdefault(name, set()).add(path.name)

    missing = {}
    for name, files in used.items():
        if name in known:
            continue
        # 直方图在 Prometheus 里以 <name>_bucket / _sum / _count 暴露，
        # 看板按分位数惯例直接写 xxx_bucket：基名有定义即视为存在。
        base = name
        for suffix in ("_bucket", "_sum", "_count"):
            if name.endswith(suffix):
                base = name[: -len(suffix)]
                break
        if base in known:
            continue
        missing[name] = files
    if missing:
        print("ERROR: 看板引用了代码与规则中都不存在的指标（OBS-07）:", file=sys.stderr)
        for name in sorted(missing):
            print(f"    {name}  (来源: {', '.join(sorted(missing[name]))})", file=sys.stderr)
        return 1
    print(f"    OK: {len(used)} 个看板指标全部有定义")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

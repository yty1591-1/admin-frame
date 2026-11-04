#!/usr/bin/env python3
"""解析 Alertmanager 路由：给定标签集，算出最终 receiver 与送达参数。

用途有两个：
  1. 值班自检（`docs/oncall/ROTATION.md` §4）：确认 P1 真的路由到 pager，
     而不是「配置文件里写了 P1」但路由树写错；
  2. Game Day / 演练（`docs/incident/GAME_DAY.md`）：离线核对剧本依赖的告警级别
     是否有可送达通道。

设计取舍：不依赖 amtool（CI 与值班笔记本不一定装），只实现 Alertmanager
文档中与判定相关的语义——matchers 的 `= != =~ !~`、`continue`、首匹配下沉。
不支持 `mute_time_intervals`（与路由选择无关）。

退出码：0 通过；1 断言失败（receiver 不符 / 只有占位地址 / 投递失败）；2 配置错误。
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

import yaml

MATCHER_RE = re.compile(r'^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(=~|!~|!=|=)\s*"([^"]*)"\s*$')
PLACEHOLDER_HOSTS = (".invalid", "example.com", "example.invalid")


class ConfigError(RuntimeError):
    """配置不可解析（不是断言失败）。"""


def parse_labels(raw: list[str]) -> dict[str, str]:
    """支持 `--labels a=1 --labels b=2` 与 `--labels a=1,b=2` 两种写法。"""
    labels: dict[str, str] = {}
    for item in raw:
        for chunk in item.split(","):
            chunk = chunk.strip()
            if not chunk:
                continue
            if "=" not in chunk:
                raise ConfigError(f"--labels 需要 k=v 形式，得到 {chunk!r}")
            key, _, value = chunk.partition("=")
            labels[key.strip()] = value.strip()
    return labels


def match_one(spec: str, labels: dict[str, str]) -> bool:
    m = MATCHER_RE.match(spec)
    if not m:
        raise ConfigError(f"无法解析 matcher {spec!r}（支持: key = \"v\" / != / =~ / !~）")
    name, op, want = m.group(1), m.group(2), m.group(3)
    have = labels.get(name, "")
    if op == "=":
        return have == want
    if op == "!=":
        return have != want
    matched = re.fullmatch(want, have) is not None
    return matched if op == "=~" else not matched


def route_matches(route: dict[str, Any], labels: dict[str, str]) -> bool:
    for spec in route.get("matchers") or []:
        if not match_one(str(spec), labels):
            return False
    # 兼容旧字段：match 为精确等值，match_re 为正则。
    for key, value in (route.get("match") or {}).items():
        if labels.get(key) != str(value):
            return False
    for key, pattern in (route.get("match_re") or {}).items():
        if re.fullmatch(str(pattern), labels.get(key, "")) is None:
            return False
    return True


def describe(route: dict[str, Any]) -> str:
    parts = [str(s) for s in (route.get("matchers") or [])]
    parts += [f"{k}={v}" for k, v in (route.get("match") or {}).items()]
    parts += [f"{k}=~{v}" for k, v in (route.get("match_re") or {}).items()]
    return "[" + ", ".join(parts) + "]" if parts else "[root]"


def resolve(route: dict[str, Any], labels: dict[str, str], chain: list[str]):
    """返回 (命中的 route, 选择链)。

    Alertmanager 语义：从上到下取第一个 matcher 命中的子路由并下沉；
    若该子路由 `continue: true`，还需继续尝试后面的兄弟路由（后命中的生效）。
    """
    chosen = (route, chain)
    for child in route.get("routes") or []:
        if not route_matches(child, labels):
            continue
        node, node_chain = resolve(child, labels, chain + [describe(child)])
        chosen = (node, node_chain)
        if not child.get("continue", False):
            break
    return chosen


def is_placeholder(url: str) -> bool:
    lowered = url.lower()
    return any(host in lowered for host in PLACEHOLDER_HOSTS)


def collect_endpoints(receivers: list[dict[str, Any]], name: str) -> list[str]:
    urls: list[str] = []
    for receiver in receivers:
        if receiver.get("name") != name:
            continue
        for key, value in receiver.items():
            if not key.endswith("_configs") or not isinstance(value, list):
                continue
            for item in value:
                if isinstance(item, dict):
                    for field in ("url", "api_url", "webhook_url"):
                        if item.get(field):
                            urls.append(str(item[field]))
    return urls


def post_alert(base_url: str, labels: dict[str, str], timeout: float) -> int:
    import datetime as dt

    now = dt.datetime.now(dt.timezone.utc)
    payload = [
        {
            "labels": {**labels, "alertname": labels.get("alertname", "PagingProbe"), "drill": "true"},
            "annotations": {"summary": "paging probe (drill=true，无需处置)"},
            "startsAt": now.isoformat().replace("+00:00", "Z"),
            "endsAt": (now + dt.timedelta(minutes=5)).isoformat().replace("+00:00", "Z"),
        }
    ]
    req = urllib.request.Request(
        base_url.rstrip("/") + "/api/v2/alerts",
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:  # noqa: S310 - 只投递到显式传入的 Alertmanager
            print(f"posted: HTTP {resp.status}")
            return 0 if 200 <= resp.status < 300 else 1
    except urllib.error.HTTPError as exc:  # pragma: no cover - 依赖外部服务
        print(f"ERROR: Alertmanager 返回 HTTP {exc.code}: {exc.read()[:200]!r}", file=sys.stderr)
        return 1
    except OSError as exc:  # pragma: no cover
        print(f"ERROR: 无法连接 Alertmanager {base_url}: {exc}", file=sys.stderr)
        return 1


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="解析 Alertmanager 路由并校验值班可达性")
    parser.add_argument("--config", default="deploy/observability/alertmanager/alertmanager.yaml")
    parser.add_argument("--labels", action="append", default=[], help="k=v，可重复")
    parser.add_argument("--expect-receiver", help="断言最终 receiver 名")
    parser.add_argument("--require-real-webhook", action="store_true", help="存在占位地址即失败")
    parser.add_argument("--allow-placeholder", action="store_true", help="占位地址只告警不失败")
    parser.add_argument("--post", action="store_true", help="向 Alertmanager 投递一条演练告警")
    parser.add_argument("--alertmanager-url", default="")
    parser.add_argument("--timeout", type=float, default=10.0)
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args(argv)

    labels = parse_labels(args.labels)
    if not labels:
        print("ERROR: 至少需要一个 --labels k=v", file=sys.stderr)
        return 2

    path = Path(args.config)
    if not path.is_file():
        print(f"ERROR: 找不到 Alertmanager 配置 {path}", file=sys.stderr)
        return 2
    try:
        doc = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
        root = doc.get("route")
        if not isinstance(root, dict):
            raise ConfigError("缺少顶层 route")
        route, chain = resolve(root, labels, ["[root]"])
        chosen = (route.get("receiver") or root.get("receiver") or "").strip()
        if not chosen:
            raise ConfigError("命中的 route 没有 receiver")
        endpoints = collect_endpoints(doc.get("receivers") or [], chosen)
    except (ConfigError, yaml.YAMLError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 2

    placeholders = [u for u in endpoints if is_placeholder(u)]
    result = {
        "labels": labels,
        "receiver": chosen,
        "chain": chain,
        "group_wait": route.get("group_wait") or root.get("group_wait"),
        "group_interval": route.get("group_interval") or root.get("group_interval"),
        "repeat_interval": route.get("repeat_interval") or root.get("repeat_interval"),
        "endpoints": endpoints,
        "placeholder": bool(endpoints) and len(placeholders) == len(endpoints),
        "placeholder_endpoints": placeholders,
    }

    if args.json:
        print(json.dumps(result, ensure_ascii=False, indent=2))
    else:
        print(f"labels:          {labels}")
        print(f"receiver:        {chosen}")
        print(f"chain:           {' > '.join(chain)}")
        print(f"group_wait:      {result['group_wait']}")
        print(f"repeat_interval: {result['repeat_interval']}")
        for url in endpoints:
            mark = "  (placeholder)" if is_placeholder(url) else ""
            print(f"endpoint:        {url}{mark}")
        if not endpoints:
            print("endpoint:        <未配置 webhook/api>")

    failed = False
    if args.expect_receiver and chosen != args.expect_receiver:
        print(f"ERROR: receiver={chosen}，期望 {args.expect_receiver}", file=sys.stderr)
        failed = True
    if result["placeholder"] and not args.allow_placeholder:
        print(
            "ERROR: 命中的 receiver 只有占位地址（*.invalid / example.com）；"
            "替换为真实 Webhook 后 P1 才真正可呼叫",
            file=sys.stderr,
        )
        failed = True
    elif result["placeholder"]:
        print("WARN: receiver 仍是占位地址（演练/自检允许，生产不可用）", file=sys.stderr)
    if args.require_real_webhook and not endpoints:
        print("ERROR: receiver 没有任何投递端点（--require-real-webhook）", file=sys.stderr)
        failed = True
    if failed:
        return 1

    if args.post:
        base = args.alertmanager_url or os.environ.get("ALERTMANAGER_URL", "")
        if not base:
            print("ERROR: --post 需要 --alertmanager-url 或 ALERTMANAGER_URL", file=sys.stderr)
            return 2
        return post_alert(base, labels, args.timeout)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

#!/usr/bin/env bash
# ENT-18：值班与事故管理门禁。
#
# 校验（OC = on-call）：
#   OC-01 事故/值班文档齐备（分级矩阵、响应流程、复盘模板、行动项台账、演练计划、排班）
#   OC-02 分级矩阵结构完整，P1 确认时限 ≤ 5 分钟
#   OC-03 ALERTS.md / Prometheus 规则里的级别都在矩阵内；P1→pager、P2/P3→ticket 路由可解析
#   OC-04 复盘模板与行动项台账字段完整（owner / 截止 / 验证 / 状态）
#   OC-05 Game Day ≥3 场景，每个引用存在的 runbook；演练记录含三段耗时字段
#   OC-06 值班文档含交接清单；runbooks/README 无「待填」占位且指向值班与事故入口
#   OC-07 PR 模板与 RUNBOOK_STANDARDS 已登记事故/值班条款（RB-08/RB-09）
#   OC-08 Alertmanager 占位地址必须在行动项台账里有 open/in_progress 跟踪项（不允许静默）
#
# 用法: bash scripts/ci/check-oncall.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

if command -v python3 >/dev/null 2>&1; then
  PY=python3
elif command -v python >/dev/null 2>&1; then
  PY=python
else
  echo "ERROR: 需要 python3 或 python" >&2
  exit 1
fi

FAIL=0
INC_DIR="docs/incident"
MATRIX="${INC_DIR}/SEVERITY_MATRIX.md"
RESPONSE="${INC_DIR}/RESPONSE.md"
POSTMORTEM="${INC_DIR}/POSTMORTEM_TEMPLATE.md"
ACTIONS="${INC_DIR}/ACTION_ITEMS.md"
GAMEDAY="${INC_DIR}/GAME_DAY.md"
ROTATION="docs/oncall/ROTATION.md"
ALERTS="docs/runbooks/ALERTS.md"

echo "==> OC-01: incident & on-call docs exist"
for f in "${MATRIX}" "${RESPONSE}" "${POSTMORTEM}" "${ACTIONS}" "${GAMEDAY}" "${ROTATION}"; do
  if [[ -f "${f}" ]]; then
    echo "    OK: ${f}"
  else
    echo "ERROR: missing ${f} (OC-01)" >&2
    FAIL=1
  fi
done
shopt -s nullglob
DRILLS=("${INC_DIR}"/drills/*.md)
shopt -u nullglob
if [[ "${#DRILLS[@]}" -eq 0 ]]; then
  echo "ERROR: no drill record under ${INC_DIR}/drills/ (OC-01/OC-05)" >&2
  FAIL=1
else
  echo "    OK: ${#DRILLS[@]} drill record(s)"
  for d in "${DRILLS[@]}"; do
    for field in 检测用时 确认用时 缓解用时; do
      if ! grep -qF "${field}" "${d}"; then
        echo "ERROR: ${d} missing '${field}' (OC-05，复盘/演练必须给出三段耗时)" >&2
        FAIL=1
      fi
    done
  done
fi

echo "==> OC-02: severity matrix structure"
if ! "${PY}" - "${MATRIX}" <<'PY'
import pathlib, re, sys

path = pathlib.Path(sys.argv[1])
if not path.is_file():
    raise SystemExit(0)
lines = path.read_text(encoding="utf-8").splitlines()

def cells(line):
    return [c.strip() for c in line.strip().strip("|").split("|")]

header = None
rows = {}
for line in lines:
    if not line.startswith("|"):
        continue
    parts = cells(line)
    if header is None and "级别" in parts[0] and "确认时限" in " ".join(parts):
        header = [re.sub(r"[*`]", "", p).strip() for p in parts]
        continue
    if header and re.match(r"^\*{0,2}P[123]\*{0,2}$", parts[0].replace("**", "").strip()):
        level = parts[0].replace("*", "").strip()
        rows[level] = parts

if not header:
    print("ERROR: 找不到分级矩阵表头（需含 级别 / 确认时限）(OC-02)", file=sys.stderr)
    raise SystemExit(1)

required = ["判定条件", "确认时限", "首次通报", "更新频率", "缓解目标", "升级时限", "通道"]
missing = [c for c in required if not any(c in h for h in header)]
if missing:
    print(f"ERROR: 矩阵缺少列 {missing} (OC-02)", file=sys.stderr)
    raise SystemExit(1)

failed = False
for level in ("P1", "P2", "P3"):
    if level not in rows:
        print(f"ERROR: 矩阵缺少 {level} 行 (OC-02)", file=sys.stderr)
        failed = True
        continue
    if len(rows[level]) != len(header):
        print(f"ERROR: {level} 行列数 {len(rows[level])} != 表头 {len(header)} (OC-02)", file=sys.stderr)
        failed = True
    for name, value in zip(header, rows[level]):
        if not value:
            print(f"ERROR: {level} 的 {name} 为空 (OC-02)", file=sys.stderr)
            failed = True

confirm = rows.get("P1", [""] * len(header))
idx = next((i for i, h in enumerate(header) if "确认时限" in h), None)
if idx is not None and not re.search(r"5\s*分钟", confirm[idx]):
    print(f"ERROR: P1 确认时限必须 ≤5 分钟，当前 '{confirm[idx]}' (OC-02)", file=sys.stderr)
    failed = True

raise SystemExit(1 if failed else 0)
PY
then
  FAIL=1
else
  echo "    OK: matrix has P1/P2/P3 with all required columns"
fi

echo "==> OC-03: severity values and paging routes"
if ! "${PY}" - "${ALERTS}" "${MATRIX}" <<'PY'
import pathlib, re, sys

alerts_path, matrix_path = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
levels = set(re.findall(r"\bP[123]\b", alerts_path.read_text(encoding="utf-8")))
matrix_levels = set(re.findall(r"\bP[123]\b", matrix_path.read_text(encoding="utf-8")))
missing = sorted(levels - matrix_levels)
if missing:
    print(f"ERROR: ALERTS.md 使用了矩阵未定义的级别 {missing} (OC-03)", file=sys.stderr)
    raise SystemExit(1)
print(f"    OK: ALERTS levels {sorted(levels)} all defined in matrix")
PY
then
  FAIL=1
fi

if ! "${PY}" - <<'PY'
import pathlib, re, sys

bad = []
for path in pathlib.Path("deploy/observability/prometheus/rules").glob("*.yaml"):
    for sev in re.findall(r"severity:\s*(P[0-9])", path.read_text(encoding="utf-8")):
        if sev not in ("P1", "P2", "P3"):
            bad.append((path.name, sev))
if bad:
    print(f"ERROR: 规则中使用了矩阵外的级别 {bad} (OC-03)", file=sys.stderr)
    raise SystemExit(1)
print("    OK: rule severities within P1/P2/P3")
PY
then
  FAIL=1
fi

for pair in "P1:pager" "P2:ticket" "P3:ticket"; do
  sev="${pair%%:*}"
  receiver="${pair##*:}"
  route_out=""
  if ! route_out="$("${PY}" scripts/ops/paging_probe.py --labels "severity=${sev}" \
      --expect-receiver "${receiver}" --allow-placeholder 2>&1)"; then
    echo "ERROR: severity=${sev} 未路由到 ${receiver} (OC-03):" >&2
    printf '%s\n' "${route_out}" >&2
    FAIL=1
  else
    echo "    OK: ${sev} → ${receiver}"
  fi
done

echo "==> OC-04: postmortem template and action ledger"
if ! "${PY}" - "${POSTMORTEM}" "${ACTIONS}" <<'PY'
import pathlib, re, sys

postmortem, actions = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
failed = False

pm = postmortem.read_text(encoding="utf-8")
for heading in ("## 1. 摘要", "## 2. 影响", "## 3. 时间线", "## 4. 根因", "## 5. 行动项"):
    if heading not in pm:
        print(f"ERROR: 复盘模板缺少章节 '{heading}' (OC-04)", file=sys.stderr)
        failed = True
for field in ("负责人", "截止", "验证方式"):
    if field not in pm:
        print(f"ERROR: 复盘模板行动项缺少字段 '{field}' (OC-04)", file=sys.stderr)
        failed = True
for marker in ("检测用时", "确认用时", "缓解用时"):
    if marker not in pm:
        print(f"ERROR: 复盘模板缺少耗时字段 '{marker}' (OC-04)", file=sys.stderr)
        failed = True

text = actions.read_text(encoding="utf-8")
header = None
statuses = set()
for line in text.splitlines():
    if not line.startswith("|"):
        continue
    parts = [c.strip() for c in line.strip().strip("|").split("|")]
    if header is None and "ID" in parts and "负责人" in parts and "状态" in parts:
        header = parts
        for token in ("截止", "验证证据"):
            if token not in parts:
                print(f"ERROR: 行动项台账缺少列 '{token}' (OC-04)", file=sys.stderr)
                failed = True
        status_idx = parts.index("状态")
        continue
    if header is None or set("".join(parts)) <= {"-", ":", " "} or parts[0] in ("—", "ID"):
        continue
    if len(parts) != len(header):
        print(f"ERROR: 行动项行字段数 {len(parts)} != {len(header)}: {line[:60]} (OC-04)", file=sys.stderr)
        failed = True
        continue
    status = parts[status_idx]
    statuses.add(status)
    if status not in ("open", "in_progress", "done", "waived"):
        print(f"ERROR: 行动项 {parts[0]} 状态非法 '{status}'（允许 open/in_progress/done/waived）(OC-04)", file=sys.stderr)
        failed = True
    if not re.match(r"^AI-\d{4}-\d{2}-\d{2}-\d{2}$", parts[0]):
        print(f"ERROR: 行动项 ID 格式非法 '{parts[0]}'（应为 AI-YYYY-MM-DD-NN）(OC-04)", file=sys.stderr)
        failed = True

if header is None:
    print("ERROR: 找不到行动项台账表头 (OC-04)", file=sys.stderr)
    failed = True
elif not statuses:
    print("ERROR: 行动项台账没有数据行 (OC-04)", file=sys.stderr)
    failed = True

raise SystemExit(1 if failed else 0)
PY
then
  FAIL=1
else
  echo "    OK: postmortem fields and action ledger complete"
fi

echo "==> OC-05: game day scenarios reference real runbooks"
if ! "${PY}" - "${GAMEDAY}" <<'PY'
import pathlib, re, sys

path = pathlib.Path(sys.argv[1])
text = path.read_text(encoding="utf-8")
sections = re.split(r"^### (G\d)\b.*$", text, flags=re.M)
# sections: [pre, 'G1', body, 'G2', body, ...]
pairs = list(zip(sections[1::2], sections[2::2]))
if len(pairs) < 3:
    print(f"ERROR: 演练计划只有 {len(pairs)} 个场景，至少需要 3 个 (OC-05)", file=sys.stderr)
    raise SystemExit(1)

failed = False
for sid, body in pairs:
    refs = re.findall(r"\]\((\.\./runbooks/[a-z0-9-]+\.md)\)", body)
    if not refs:
        print(f"ERROR: 场景 {sid} 未引用任何 runbook (OC-05)", file=sys.stderr)
        failed = True
        continue
    for ref in refs:
        if not (pathlib.Path("docs/incident") / ref).resolve().exists():
            print(f"ERROR: 场景 {sid} 引用的 runbook 不存在: {ref} (OC-05)", file=sys.stderr)
            failed = True
raise SystemExit(1 if failed else 0)
PY
then
  FAIL=1
else
  echo "    OK: scenarios reference existing runbooks"
fi

echo "==> OC-06: rotation handoff and entry points"
if [[ ! -f "${ROTATION}" ]]; then
  FAIL=1
else
  for token in "## 1. 轮换模型" "## 3. 交接清单" "## 4. 呼叫链路验证" "## 5. 升级阶梯"; do
    if ! grep -qF "${token}" "${ROTATION}"; then
      echo "ERROR: ${ROTATION} 缺少章节 '${token}' (OC-06)" >&2
      FAIL=1
    fi
  done
  for token in "未关闭的 P1/P2" "silence" "发布冻结"; do
    if ! grep -qF "${token}" "${ROTATION}"; then
      echo "ERROR: 交接清单缺少 '${token}' (OC-06)" >&2
      FAIL=1
    fi
  done
fi

README="docs/runbooks/README.md"
if [[ ! -f "${README}" ]]; then
  echo "ERROR: missing ${README} (OC-06)" >&2
  FAIL=1
else
  if grep -qF "待填" "${README}"; then
    echo "ERROR: ${README} 仍有「待填」占位；值班入口必须指向具体文档 (OC-06)" >&2
    FAIL=1
  fi
  for ref in "../oncall/ROTATION.md" "../incident/RESPONSE.md" "../incident/SEVERITY_MATRIX.md"; do
    if ! grep -qF "${ref}" "${README}"; then
      echo "ERROR: ${README} 未链接 ${ref} (OC-06)" >&2
      FAIL=1
    fi
  done
  echo "    OK: rotation handoff + runbook entry points"
fi

echo "==> OC-07: PR template and RUNBOOK_STANDARDS register the clause"
# 双平台模板必须同步：只改 GitHub 会让 Gitee MR 绕过事故自检。
for PR_TEMPLATE in .github/PULL_REQUEST_TEMPLATE.md .gitee/PULL_REQUEST_TEMPLATE.md; do
  if [[ ! -f "${PR_TEMPLATE}" ]] || ! grep -qF "事故与值班自检" "${PR_TEMPLATE}"; then
    echo "ERROR: ${PR_TEMPLATE} 缺少「事故与值班自检」小节 (OC-07)" >&2
    FAIL=1
  else
    echo "    OK: ${PR_TEMPLATE} has incident/on-call checklist"
  fi
done
for rule in RB-08 RB-09; do
  if ! grep -qF "${rule}" docs/RUNBOOK_STANDARDS.md; then
    echo "ERROR: docs/RUNBOOK_STANDARDS.md 缺少 ${rule} (OC-07)" >&2
    FAIL=1
  fi
done

echo "==> OC-08: alertmanager placeholders must be tracked"
if "${PY}" - "${ACTIONS}" <<'PY'
import pathlib, re, sys

am = pathlib.Path("deploy/observability/alertmanager/alertmanager.yaml")
if not am.is_file():
    print("ERROR: missing alertmanager.yaml (OC-08)", file=sys.stderr)
    raise SystemExit(1)
if ".invalid" not in am.read_text(encoding="utf-8"):
    print("    OK: no placeholder endpoint")
    raise SystemExit(0)

text = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
tracked = False
for line in text.splitlines():
    low = line.lower()
    if "alertmanager" in low and re.search(r"\|\s*(open|in_progress)\s*\|", low):
        tracked = True
        break
if not tracked:
    print("ERROR: alertmanager 仍有占位地址，但 ACTION_ITEMS.md 没有 open/in_progress 跟踪项 (OC-08)", file=sys.stderr)
    raise SystemExit(1)
print("    OK: placeholder is tracked by an open action item")
PY
then
  :
else
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "on-call check failed (ENT-18 / RUNBOOK_STANDARDS RB-08/RB-09)" >&2
  exit 1
fi
echo "OK: oncall"

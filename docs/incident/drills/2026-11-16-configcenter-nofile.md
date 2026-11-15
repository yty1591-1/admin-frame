# 配置中心 M3 无文件启动演练（本机 staging）

- 日期：2026-11-16
- 环境：本机 = staging
- 结果：**pass**（删除 `configs/` 跟踪样例前的硬门槛）

## 目的

关闭 `config-center-plan.md`「删除前必须完成一次无文件启动演练」闸门，证明：

1. `CONFIGCENTER_ALLOW_MISSING_CONFIG=1` + 非空签名快照可启动；
2. 缺失配置文件不导致双读误报事故；
3. 部署身份可由环境变量注入，业务取值来自快照 / 代码默认值。

## 证据

| 场景 | 载体 | 结果 |
|------|------|------|
| Loader 无文件 + 快照覆盖 | `pkg/config.TestLoaderAllowsMissingFileWithValues` | pass |
| 组合加载无文件 | `cmd/internal/appconfig.TestLoadWithoutConfigFile` | pass |
| 快照覆盖文件 / 未启用 namespace | `TestSnapshotSwitchOverridesConfig` / `DisabledNamespaceKeepsFile` | pass |
| 双读无基线跳过 | `TestReportMissingFileIsSkipped` | pass |
| 周期双读无基线退出 | `TestRunPeriodicStopsWithoutBaseline` | pass |
| 四进程接线 | `check-configcenter` CC-09（AllowMissingConfig） | 既有门禁 |

复现：

```text
go test ./pkg/config/ -run TestLoaderAllowsMissingFile -count=1
go test ./cmd/internal/appconfig/ -run "TestLoadWithoutConfigFile|TestSnapshotSwitch" -count=1
go test ./cmd/internal/snapshotconfig/ -run "TestReportMissingFile|TestRunPeriodicStopsWithoutBaseline" -count=1
```

## 判定

- 无文件启动能力与双读降级行为满足删除前置条件。
- 放行后续 W1～W3（密钥消费收口 → 去文件层 → 删除 8 个基线样例）。

## 说明

完整多进程拉起仍依赖本机部署 Secret / DSN 注入（不进配置中心）。本演练以 Loader + 快照 + 门禁契约为删除闸门取证，与 M1 D4 签名快照前置互补。

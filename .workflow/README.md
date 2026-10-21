# Gitee Go 流水线

本目录为 **Gitee Go** 流水线描述（与 GitHub Actions **不等价语法**，检查项对齐）。

| 文件 | 触发 |
|------|------|
| `PRPipeline.yml` | 指向 `main` / `master` 的 Pull Request |
| `MasterPipeline.yml` | push 到 `main` / `master` |
| `BranchPipeline.yml` | push 到其它分支 |

命令入口统一为 `scripts/ci/gitee-job.sh`，与 `.github/workflows/ci.yml`、本地 `make ci` 同一套门禁（含 secrets、openapi）。

## 发版镜像（当前仅 GitHub）

镜像构建/推送/扫描在 `.github/workflows/image.yml`，GitHub 侧触发（push `main`/`master`、`v*` tag）。
Gitee 端**暂未接入**（需组织 registry 凭据与 `build@docker` 等价能力），因此 Gitee 流水线只做质量门禁。
接入时须同步 `docs/CI_STANDARDS.md` §3.1 映射表，并保持 RELS 约束（无 `:latest`、扫描、版本注入）。

## 开通步骤

1. 在 Gitee 仓库开通 **Gitee Go**。
2. 确认本目录三个 YAML 已被流水线识别（或在控制台「从代码库导入」）。
3. 将 YAML 中 `GO_VERSION` / `golangVersion` 改为控制台 **Golang 构建插件**当前可用的最高版本。
4. 在仓库设置中开启「合并前必须通过流水线」（名称以控制台为准）。

## Go 版本注意

公共文档中云端 `build@golang` 可选版本可能低于本仓库 `go.mod`。若无法选到满足 `go.mod` 的版本：

- 使用企业版/自建构建机，在 Agent 上安装匹配的 Go 后执行 `make ci`；或
- 调整插件镜像（组织私有构建环境）。

详见 `docs/CI_STANDARDS.md` §3「多平台」。

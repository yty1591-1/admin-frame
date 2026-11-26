# M2/M3 密钥类样例盘点（已删除）

来源：历史 `scripts/ci/configs-legacy-baseline.txt`（M3 已清空为注释）。

| 文件（已删） | 迁入策略 |
|------|----------|
| `configs/*/env.example` | 基础设施口令不入配置中心；仅部署注入 / 本地未跟踪 `.env` |
| `configs/prod/modules/auth.yaml.example` | JWT → `config_secret`（Q9）+ `secretruntime` |
| `configs/prod/modules/identity.yaml.example` | 身份域凭据 → 部署 Secret |
| `configs/prod/modules/infra.yaml.example` | DB/Redis 口令**不**入配置中心 |
| `configs/prod/modules/notification.yaml.example` | SMTP/短信 → `*_env` 部署注入 |
| `configs/prod/modules/platform.yaml.example` | ENT-07 Provider + 业务密钥走中心 |

自动生成样例键：`platform.demo.hmac_key`、`auth.jwt.private_key`（`secret_rule.auto_generate=true`）。

## M3 消费收口（2026-11-16）— 已完成删除

| 类别 | 删文件后取值来源 |
|------|------------------|
| JWT HS256 secret | ENT-07 Provider / env；空则 `secretruntime.ApplyJWTSecret` 读 `auth.jwt.private_key` |
| identity hash / MFA | 部署 env / Secret 挂载（`ADMIN_FRAME_IDENTITY_*`） |
| notification SMTP/SMS | `*_env` 部署注入；非密钥字段走代码默认值 + 快照 |
| DB / Redis 口令 | 部署 Secret；**不**进配置中心 |

`git ls-files configs`：**空**（CC-F-43）。

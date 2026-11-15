# M2 密钥类样例盘点（迁入中心，文件留 M3 删除）

来源：`scripts/ci/configs-legacy-baseline.txt`

| 文件 | 迁入策略 |
|------|----------|
| `configs/dev/env.example` | 基础设施口令不入配置中心；仅作部署注入模板 |
| `configs/prod/env.example` | 同上 |
| `configs/staging/env.example` | 同上 |
| `configs/prod/modules/auth.yaml.example` | JWT / 哈希密钥 → `config_secret`（Q9） |
| `configs/prod/modules/identity.yaml.example` | 身份域凭据 → secret + grant |
| `configs/prod/modules/infra.yaml.example` | 基础设施段：DB/Redis 口令**不**入配置中心 |
| `configs/prod/modules/notification.yaml.example` | SMTP / 短信 AK → `config_secret` |
| `configs/prod/modules/platform.yaml.example` | `secrets:` ENT-07 段仍为启动注入；业务密钥走中心 |

自动生成样例键：`platform.demo.hmac_key`（`secret_rule.auto_generate=true`）。

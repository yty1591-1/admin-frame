---
title: Avoid Whole-Library Imports
impact: CRITICAL
impactDescription: 整包导入会拖慢构建、破坏按需加载并撑大入口 chunk
tags: bundle, imports, tree-shaking, performance
---

## 避免整包导入

只导入实际使用的符号，避免 `import * as`、默认导入整包工具库、整包图标库。Vite 对 ESM 依赖可做 tree-shaking，但整包导入仍会增加模块图分析时间与入口体积。

**项目约束优先（FE-04）**：跨层引用必须走 `index.ts` 公共出口。本规则用于识别第三方库整包导入与无谓聚合，**不得**据此改为 `@admin-frame/*/src/*` 深链，也不适用于 `@admin-frame/*` 的公共出口。

**Incorrect：**

```ts
import * as antd from 'antd'                 // 整包
import { Table } from 'antd'                 // 业务代码禁止直接导入 antd
import _ from 'lodash'                       // 整包工具库
import { format } from 'date-fns'            // 未使用子路径时可能拉入更多模块
```

**Correct：**

```ts
import { RoleTable } from '@admin-frame/ui'  // 走设计系统公共出口
import { formatMoney } from '@admin-frame/shared'
import { format } from 'date-fns/format'
```

**检查方式：**

```bash
cd frontend && pnpm size
cd frontend && pnpm build
```

入口 chunk 接近预算时，优先检查是否新增了整包依赖或把本应随路由懒加载的模块提前引入。

参考：Vercel Engineering React Best Practices（见 [../ATTRIBUTION.md](../ATTRIBUTION.md)）。

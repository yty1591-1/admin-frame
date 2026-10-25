# 来源与许可

- 上游仓库：https://github.com/vercel-labs/agent-skills
- 上游路径：`skills/react-best-practices`
- 固定提交：`063bee94c3f4df8453406c830b0a7df0f2860278`
- 上游声明许可：MIT（见上游 `SKILL.md` frontmatter）
- 版权：Copyright (c) Vercel Engineering

## 本地修改

- 仅保留 44 条适用于 Vite SPA 的客户端规则。
- 移除 `server-*`、Next.js/RSC、SSR 水合、`next/dynamic`、SWR、`better-all` 等规则。
- 重写 `SKILL.md` 的描述、分类表与项目约束，补充与 FE-04、Token 存储、体积门禁、AntD 表格相关的例外说明。
- 规则文件内容保持上游原文；如与项目规则冲突，以 `.trae/rules/frontend.md` 为准。

## MIT License

Copyright (c) Vercel Engineering

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

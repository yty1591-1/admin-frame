---
'@vben/common-ui': minor
'@vben/locales': minor
'@vben/request': minor
---

feat(profile): 安全设置页改为展示接口真实状态

- `ProfileSecuritySetting` 由开关改为「状态徽标 + 操作入口」，新增 `SecuritySchemaItem` 与 `action` 事件，移除无效的开关交互
- `profile` 语言包补齐安全状态、占位与重试文案（zh-CN / en-US）
- 请求客户端新增 `skipErrorMessage` 选项，供探测类请求跳过全局错误提示

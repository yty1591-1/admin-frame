---
'@vben/common-ui': minor
'@vben/locales': minor
---

feat(profile): 个人中心改为响应式三页签并新增登录设备列表

- `Profile` 外壳改 `flex-col lg:flex-row`：桌面纵向页签、移动端同一 `v-model` 的横向滚动页签，内容卡 `p-4 sm:p-6 lg:p-8`
- `ProfileBaseSetting` 新增可选 `infoItems`，只读信息改用 `VbenDescriptions` 渲染
- 新增 `ProfileSessionList` 与 `SessionItem`：桌面行式 / 移动卡片式，含当前设备标记、空态、错误态、骨架与「加载更多」
- `profile` 语言包补齐昵称、设备会话与降级提示文案（zh-CN / en-US）

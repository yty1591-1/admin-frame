/**
 * IAM 高风险写操作收到 40900 时必须转审批引导（§7.3、D2-05）：
 * 关闭危险确认弹窗 → 展示审批说明与 payload 摘要；
 * 禁止按普通冲突自动重试，申请人不得审批自己的申请（SoD）。
 */
export { isApprovalRequired } from '@admin-frame/api-client';

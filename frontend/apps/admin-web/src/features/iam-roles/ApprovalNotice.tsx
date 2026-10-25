import { ConfirmDialog, Descriptions, Typography } from '@admin-frame/ui';

export type ApprovalNoticeProps = {
  open: boolean;
  roleName: string;
  roleCode: string;
  /** 预填的变更原因，作为审批单摘要展示。 */
  reason: string;
  onClose: () => void;
};

/**
 * 40900 审批引导（D2-05）：说明操作已转审批并预填 payload 摘要，
 * 不跳转、不自动重试；申请人不得审批自己的申请（SoD）。
 */
export function ApprovalNotice({ open, roleName, roleCode, reason, onClose }: ApprovalNoticeProps) {
  return (
    <ConfirmDialog
      open={open}
      title="该操作需审批"
      confirmText="我知道了"
      hideCancel
      content={
        <>
          <Typography.Paragraph>
            删除角色「{roleName}」属于高风险变更，已转为审批申请，不会立即生效。
          </Typography.Paragraph>
          <Descriptions
            column={1}
            size="small"
            items={[
              { key: 'action', label: '操作', children: '删除角色' },
              { key: 'role', label: '角色', children: `${roleName}（${roleCode}）` },
              { key: 'reason', label: '原因', children: reason },
            ]}
          />
          <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
            申请进度请在「IAM 变更审批」中查看；按职责分离要求，申请人不得审批自己的申请。
          </Typography.Paragraph>
        </>
      }
      onConfirm={onClose}
      onCancel={onClose}
    />
  );
}

import { useI18n } from '@admin-frame/i18n';
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
  const { t } = useI18n();

  return (
    <ConfirmDialog
      open={open}
      title={t('iam.approval.title')}
      confirmText={t('iam.approval.confirm')}
      hideCancel
      content={
        <>
          <Typography.Paragraph>
            {t('iam.approval.content', { name: roleName })}
          </Typography.Paragraph>
          <Descriptions
            column={1}
            size="small"
            items={[
              {
                key: 'action',
                label: t('iam.approval.actionLabel'),
                children: t('iam.role.deleteTitle'),
              },
              {
                key: 'role',
                label: t('iam.approval.roleLabel'),
                children: t('common.targetWithCode', { name: roleName, code: roleCode }),
              },
              { key: 'reason', label: t('iam.approval.reasonLabel'), children: reason },
            ]}
          />
          <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
            {t('iam.approval.hint')}
          </Typography.Paragraph>
        </>
      }
      onConfirm={onClose}
      onCancel={onClose}
    />
  );
}

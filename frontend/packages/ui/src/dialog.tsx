import { Button, Modal } from 'antd';
import type { ReactNode } from 'react';

export type ConfirmDialogProps = {
  open: boolean;
  title: string;
  content: ReactNode;
  confirmText?: string | undefined;
  danger?: boolean | undefined;
  loading?: boolean | undefined;
  onConfirm: () => void;
  onCancel: () => void;
};

/**
 * 确认弹窗（§3.5）：基于 antd Modal 的薄封装，统一焦点陷阱、Esc 关闭与按钮语义。
 * 危险操作请使用 DangerActionConfirm，它会额外收集原因并展示影响范围。
 */
export function ConfirmDialog({
  open,
  title,
  content,
  confirmText = '确定',
  danger = false,
  loading = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  return (
    <Modal
      open={open}
      title={title}
      onCancel={onCancel}
      destroyOnHidden
      footer={[
        <Button key="cancel" onClick={onCancel} disabled={loading}>
          取消
        </Button>,
        <Button key="confirm" type="primary" danger={danger} loading={loading} onClick={onConfirm}>
          {confirmText}
        </Button>,
      ]}
    >
      {content}
    </Modal>
  );
}

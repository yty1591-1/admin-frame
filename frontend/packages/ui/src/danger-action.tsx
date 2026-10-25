import { Alert, Button, Input, Modal, Space, Typography } from 'antd';
import { useEffect, useId, useState } from 'react';

const DEFAULT_REASON_MAX_LENGTH = 200;

export type DangerActionError = {
  message: string;
  traceId?: string | undefined;
};

export type DangerActionProps = {
  open: boolean;
  /** 操作名称，如「删除角色」。 */
  title: string;
  /** 作用对象名称或标识。 */
  target: string;
  /** 影响范围，逐条展示。 */
  impacts: readonly string[];
  /** IAM 写操作默认必填（D2-04）。 */
  reasonRequired?: boolean | undefined;
  reasonMaxLength?: number | undefined;
  loading?: boolean | undefined;
  error?: DangerActionError | null | undefined;
  onConfirm: (reason: string) => void;
  onCancel: () => void;
};

/**
 * 高风险操作确认（§3.7）：影响说明 + 原因收集（≤200 字）+ 红色主按钮。
 * 幂等键由调用方生成并在重试中复用；40900 由调用方转审批引导，不在此处理。
 */
export function DangerActionConfirm({
  open,
  title,
  target,
  impacts,
  reasonRequired = true,
  reasonMaxLength = DEFAULT_REASON_MAX_LENGTH,
  loading = false,
  error,
  onConfirm,
  onCancel,
}: DangerActionProps) {
  const reasonId = useId();
  const [reason, setReason] = useState('');
  // 每次打开都清空原因，避免上一次草稿被误提交。
  useEffect(() => {
    if (open) {
      setReason('');
    }
  }, [open]);

  const trimmedReason = reason.trim();
  const confirmDisabled = reasonRequired && trimmedReason === '';

  const handleCancel = (): void => {
    if (!loading) {
      onCancel();
    }
  };

  return (
    <Modal
      open={open}
      title={title}
      onCancel={handleCancel}
      closable={!loading}
      maskClosable={!loading}
      destroyOnHidden
      footer={[
        <Button key="cancel" onClick={handleCancel} disabled={loading}>
          取消
        </Button>,
        <Button
          key="confirm"
          type="primary"
          danger
          loading={loading}
          disabled={confirmDisabled}
          onClick={() => {
            onConfirm(trimmedReason);
          }}
        >
          {title}
        </Button>,
      ]}
    >
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        <Typography.Paragraph style={{ marginBottom: 0 }}>
          该操作将对「<Typography.Text strong>{target}</Typography.Text>」生效，且不可撤销。
        </Typography.Paragraph>
        {impacts.length === 0 ? null : (
          <div>
            <Typography.Text type="secondary">影响范围</Typography.Text>
            <ul style={{ margin: '8px 0 0', paddingLeft: 20 }}>
              {impacts.map((impact) => (
                <li key={impact}>{impact}</li>
              ))}
            </ul>
          </div>
        )}
        <div>
          <label htmlFor={reasonId} style={{ display: 'block', marginBottom: 8 }}>
            操作原因{reasonRequired ? '（必填，将写入审计）' : '（选填）'}
          </label>
          <Input.TextArea
            id={reasonId}
            aria-required={reasonRequired}
            value={reason}
            rows={3}
            maxLength={reasonMaxLength}
            showCount
            disabled={loading}
            placeholder="说明本次变更原因"
            onChange={(event) => {
              setReason(event.target.value);
            }}
          />
        </div>
        {error == null ? null : (
          <Alert
            type="error"
            showIcon
            message={error.message}
            description={
              error.traceId === undefined ? null : (
                <Typography.Text type="secondary" copyable={{ text: error.traceId }}>
                  trace_id: {error.traceId}
                </Typography.Text>
              )
            }
          />
        )}
      </Space>
    </Modal>
  );
}

import { Typography } from 'antd';
import { cloneElement, isValidElement, useId, type ReactNode } from 'react';

export type FormSectionProps = {
  title: string;
  description?: string | undefined;
  children: ReactNode;
};

/** 表单分区：只负责标题层级与间距，具体控件由业务提供。 */
export function FormSection({ title, description, children }: FormSectionProps) {
  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div>
        <Typography.Title level={2} style={{ margin: 0, fontSize: 16 }}>
          {title}
        </Typography.Title>
        {description === undefined ? null : (
          <Typography.Text type="secondary">{description}</Typography.Text>
        )}
      </div>
      {children}
    </section>
  );
}

export type FormFieldProps = {
  label: string;
  htmlFor: string;
  required?: boolean | undefined;
  /** 帮助文案；通过 aria-describedby 关联到控件。 */
  hint?: string | undefined;
  /** 字段错误；通过 role="alert" 播报。 */
  error?: string | undefined;
  children: ReactNode;
};

type ControlProps = {
  'aria-describedby'?: string | undefined;
  'aria-invalid'?: boolean | undefined;
  'aria-required'?: boolean | undefined;
};

/**
 * 字段布局与可访问性（§3.4）：label[for]、必填标识、hint/error 关联。
 * 校验与 RHF 绑定留在页面/feature，ui 不引入 react-hook-form。
 */
export function FormField({ label, htmlFor, required, hint, error, children }: FormFieldProps) {
  const baseId = useId();
  const hintId = `${baseId}-hint`;
  const errorId = `${baseId}-error`;
  const describedBy = [hint === undefined ? null : hintId, error === undefined ? null : errorId]
    .filter((item): item is string => item !== null)
    .join(' ');

  const control: ReactNode = isValidElement<ControlProps>(children)
    ? cloneElement(children, {
        'aria-describedby': describedBy === '' ? undefined : describedBy,
        'aria-invalid': error === undefined ? undefined : true,
        'aria-required': required === true ? true : undefined,
      })
    : children;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <label htmlFor={htmlFor} style={{ fontWeight: 500 }}>
        {label}
        {required === true ? (
          <span aria-hidden style={{ marginLeft: 4 }}>
            *
          </span>
        ) : null}
      </label>
      {control}
      {hint === undefined ? null : (
        <Typography.Text id={hintId} type="secondary" style={{ fontSize: 12 }}>
          {hint}
        </Typography.Text>
      )}
      {error === undefined ? null : (
        <Typography.Text id={errorId} type="danger" role="alert" style={{ fontSize: 12 }}>
          {error}
        </Typography.Text>
      )}
    </div>
  );
}

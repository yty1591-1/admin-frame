import '@testing-library/jest-dom/vitest';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { FormField, FormSection } from './form-layout';

describe('FormSection', () => {
  it('以 h2 渲染分区标题并展示描述', () => {
    render(
      <FormSection title="基本信息" description="用于身份识别">
        <p>字段区</p>
      </FormSection>,
    );

    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('基本信息');
    expect(screen.getByText('用于身份识别')).toBeInTheDocument();
    expect(screen.getByText('字段区')).toBeInTheDocument();
  });

  it('无描述时不渲染说明文案', () => {
    render(
      <FormSection title="安全设置">
        <p>字段区</p>
      </FormSection>,
    );

    expect(screen.queryByText('用于身份识别')).not.toBeInTheDocument();
  });
});

describe('FormField', () => {
  it('关联 label、必填标识与控件可访问属性', () => {
    render(
      <FormField label="账号" htmlFor="login-subject" required>
        <input id="login-subject" />
      </FormField>,
    );

    expect(screen.getByLabelText(/账\s*号/)).toHaveAttribute('aria-required', 'true');
    expect(screen.getByLabelText(/账\s*号/)).not.toHaveAttribute('aria-invalid');
  });

  it('hint 与 error 通过 aria-describedby 关联并播报错误', () => {
    render(
      <FormField label="角色名称" htmlFor="role-name" hint="2-64 个字符" error="请输入角色名称">
        <input id="role-name" />
      </FormField>,
    );

    const input = screen.getByLabelText('角色名称');
    const hint = screen.getByText('2-64 个字符');
    const error = screen.getByRole('alert');

    expect(error).toHaveTextContent('请输入角色名称');
    expect(input.getAttribute('aria-describedby')).toBe(
      `${hint.getAttribute('id') ?? ''} ${error.getAttribute('id') ?? ''}`,
    );
    expect(input).toHaveAttribute('aria-invalid', 'true');
  });
});

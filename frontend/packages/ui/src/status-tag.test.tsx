import '@testing-library/jest-dom/vitest';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { StatusTag } from './status-tag';

describe('StatusTag', () => {
  it('success 使用语义色且文本本身承载含义', () => {
    render(<StatusTag tone="success" label="启用" />);

    const tag = screen.getByText('启用');
    expect(tag).toHaveClass('ant-tag-green');
  });

  it('default 不附加语义色', () => {
    render(<StatusTag tone="default" label="已废弃" />);

    expect(screen.getByText('已废弃')).not.toHaveClass('ant-tag-green');
  });
});

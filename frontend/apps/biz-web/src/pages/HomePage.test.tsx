import { defaultTranslator } from '@admin-frame/i18n';
import { findSeriousA11yViolations, renderWithProviders } from '@admin-frame/testing';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import HomePage from './HomePage';

const t = defaultTranslator;

describe('HomePage (biz)', () => {
  it('渲染商户工作台卡片与契约版本', async () => {
    const { container } = renderWithProviders(<HomePage />, { toast: false });

    expect(screen.getByText(t('common.homeCardTitle'))).toBeInTheDocument();
    expect(
      screen.getByText(t('common.contractVersion', { commit: 'test-commit' })),
    ).toBeInTheDocument();
    expect(await findSeriousA11yViolations(container)).toEqual([]);
  });
});

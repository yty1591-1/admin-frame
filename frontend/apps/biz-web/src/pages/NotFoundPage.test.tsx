import { defaultTranslator } from '@admin-frame/i18n';
import { findSeriousA11yViolations, renderWithProviders } from '@admin-frame/testing';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import NotFoundPage from './NotFoundPage';

const t = defaultTranslator;

describe('NotFoundPage (biz)', () => {
  it('渲染 404 提示且无严重可访问性问题', async () => {
    const { container } = renderWithProviders(<NotFoundPage />, { toast: false });

    expect(screen.getByText(t('nav.notFound'))).toBeInTheDocument();
    expect(screen.getByText(t('common.notFoundDescriptionBiz'))).toBeInTheDocument();
    expect(await findSeriousA11yViolations(container)).toEqual([]);
  });
});

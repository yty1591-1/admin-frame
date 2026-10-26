import '@testing-library/jest-dom/vitest';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { I18nProvider, useI18n } from './i18n-provider';
import { createTranslator } from './translate';

function Probe() {
  const { locale, t } = useI18n();
  return (
    <span>
      {locale}:{t('common.retry')}
    </span>
  );
}

describe('I18nProvider', () => {
  it('无 Provider 时回退默认 zh-CN 翻译器', () => {
    render(<Probe />);
    expect(screen.getByText('zh-CN:重试')).toBeInTheDocument();
  });

  it('Provider 注入自定义翻译器', () => {
    const translator = createTranslator({ 'common.retry': 'Retry' });
    render(
      <I18nProvider translator={translator}>
        <Probe />
      </I18nProvider>,
    );
    expect(screen.getByText('zh-CN:Retry')).toBeInTheDocument();
  });
});

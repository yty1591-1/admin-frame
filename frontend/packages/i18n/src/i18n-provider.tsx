import { createContext, useContext, useMemo, type ReactNode } from 'react';
import { createTranslator, defaultTranslator, type Translator } from './translate';

export type I18nValue = {
  locale: 'zh-CN';
  t: Translator;
};

const I18nContext = createContext<I18nValue>({ locale: 'zh-CN', t: defaultTranslator });

/**
 * 字典 Provider（S3 / §3.4）：必须早于 ToastProvider / RouterProvider 装配。
 * 不提供 Provider 时 `useI18n` 回退默认 zh-CN 翻译器，保证设计系统可独立测试。
 */
export function I18nProvider({
  children,
  translator,
}: {
  children: ReactNode;
  translator?: Translator | undefined;
}) {
  const value = useMemo<I18nValue>(
    () => ({ locale: 'zh-CN', t: translator ?? createTranslator() }),
    [translator],
  );
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nValue {
  return useContext(I18nContext);
}

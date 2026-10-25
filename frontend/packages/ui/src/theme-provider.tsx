import { ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import type { ReactNode } from 'react';
import { themeTokens } from './tokens';

export type ThemeProviderProps = {
  children: ReactNode;
  /** 部署期注入的 CSP nonce；未注入时由静态策略兜底（§10.5）。 */
  cspNonce?: string | undefined;
};

export function ThemeProvider({ children, cspNonce }: ThemeProviderProps) {
  const csp = cspNonce === undefined ? {} : { csp: { nonce: cspNonce } };
  return (
    <ConfigProvider locale={zhCN} theme={{ token: themeTokens }} {...csp}>
      {children}
    </ConfigProvider>
  );
}

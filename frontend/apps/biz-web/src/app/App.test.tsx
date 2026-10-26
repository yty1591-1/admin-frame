import { defaultTranslator } from '@admin-frame/i18n';
import type { AppRuntimeConfig } from '@admin-frame/shared';
import { renderWithProviders } from '@admin-frame/testing';
import { screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { App } from './App';

const t = defaultTranslator;

const CONFIG: AppRuntimeConfig = {
  apiBaseUrl: 'http://api.test',
  env: 'local',
  channel: 'biz',
  features: {},
  monitoring: { dsn: null, sampleRate: 0, release: null },
};

afterEach(() => {
  window.history.replaceState(null, '', '/');
});

describe('App (biz)', () => {
  it('渲染商户后台外壳与工作台', async () => {
    renderWithProviders(<App config={CONFIG} />, { toast: false });

    expect(await screen.findByText(t('common.appNameBiz'))).toBeInTheDocument();
    expect(await screen.findByText(t('common.homeCardTitle'))).toBeInTheDocument();
  });

  it('未知路径渲染 404 而不是空白页', async () => {
    window.history.replaceState(null, '', '/not-registered');

    renderWithProviders(<App config={CONFIG} />, { toast: false });

    expect(await screen.findByText(t('nav.notFound'))).toBeInTheDocument();
  });
});

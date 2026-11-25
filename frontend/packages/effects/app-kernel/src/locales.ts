import type { Locale } from 'ant-design-vue/es/locale';

import type { App } from 'vue';

import type { LocaleSetupOptions, SupportedLanguagesType } from '@vben/locales';

import { ref } from 'vue';

import {
  $t,
  setupI18n as coreSetup,
  loadLocalesMapFromDir,
} from '@vben/locales';
import { preferences } from '@vben/preferences';

import antdEnLocale from 'ant-design-vue/es/locale/en_US';
import antdDefaultLocale from 'ant-design-vue/es/locale/zh_CN';
import dayjs from 'dayjs';

export interface CreateLocaleSetupOptions {
  /** 各应用 import.meta.glob 采集到的语言包模块 */
  modules: Record<string, () => Promise<unknown>>;
}

/**
 * 创建 i18n 装配器：语言包模块由应用注入，装配逻辑收敛到内核。
 */
export function createLocaleSetup({ modules }: CreateLocaleSetupOptions) {
  const antdLocale = ref<Locale>(antdDefaultLocale);

  const localesMap = loadLocalesMapFromDir(
    /\.\/langs\/([^/]+)\/(.*)\.json$/,
    modules,
  );

  /**
   * 加载应用特有的语言包
   */
  async function loadMessages(lang: SupportedLanguagesType) {
    const [appLocaleMessages] = await Promise.all([
      localesMap[lang]?.(),
      loadThirdPartyMessage(lang),
    ]);
    return appLocaleMessages?.default;
  }

  /**
   * 加载第三方组件库的语言包
   */
  async function loadThirdPartyMessage(lang: SupportedLanguagesType) {
    await Promise.all([loadAntdLocale(lang), loadDayjsLocale(lang)]);
  }

  /**
   * 加载 dayjs 的语言包
   */
  async function loadDayjsLocale(lang: SupportedLanguagesType) {
    switch (lang) {
      case 'en-US': {
        await import('dayjs/locale/en');
        dayjs.locale('en');
        break;
      }
      case 'zh-CN': {
        await import('dayjs/locale/zh-cn');
        dayjs.locale('zh-cn');
        break;
      }
      // 默认使用英语
      default: {
        await import('dayjs/locale/en');
        dayjs.locale('en');
      }
    }
  }

  /**
   * 加载 antd 的语言包
   */
  async function loadAntdLocale(lang: SupportedLanguagesType) {
    switch (lang) {
      case 'en-US': {
        antdLocale.value = antdEnLocale;
        break;
      }
      case 'zh-CN': {
        antdLocale.value = antdDefaultLocale;
        break;
      }
    }
  }

  async function setupI18n(app: App, options: LocaleSetupOptions = {}) {
    await coreSetup(app, {
      defaultLocale: preferences.app.locale,
      loadMessages,
      missingWarn: !import.meta.env.PROD,
      ...options,
    });
  }

  return { $t, antdLocale, setupI18n };
}

import { createLocaleSetup } from '@vben/app-kernel/locales';

const modules = import.meta.glob('./langs/**/*.json');

const { $t, antdLocale, setupI18n } = createLocaleSetup({ modules });

export { $t, antdLocale, setupI18n };

import { bootstrapApp, createAppRoot } from '@vben/app-kernel';

import { antdLocale, setupI18n } from '#/locales';

import { router } from './router';

async function bootstrap(namespace: string) {
  await bootstrapApp({
    namespace,
    router,
    rootComponent: createAppRoot({ antdLocale }),
    setupI18n,
  });
}

export { bootstrap };

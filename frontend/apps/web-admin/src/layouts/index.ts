const BasicLayout = () => import('./basic.vue');
const AuthPageLayout = () =>
  import('@vben/app-kernel/layouts/auth').then((m) => m.default);

const IFrameView = () => import('@vben/layouts').then((m) => m.IFrameView);

export { AuthPageLayout, BasicLayout, IFrameView };

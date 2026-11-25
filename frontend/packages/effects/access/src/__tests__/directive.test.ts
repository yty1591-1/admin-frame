import type { Pinia } from 'pinia';

import type { App } from 'vue';

import {
  createApp,
  defineComponent,
  h,
  nextTick,
  ref,
  resolveDirective,
  withDirectives,
} from 'vue';

import { useAccessStore } from '@vben/stores';

import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { registerAccessDirective } from '../directive';

const codes = ref<string[]>(['iam:role:write']);

const Demo = defineComponent({
  setup() {
    return () => {
      const access = resolveDirective('access');
      const button = h('button', 'save');
      if (!access) {
        return button;
      }
      return withDirectives(button, [[access, codes.value, 'code']]);
    };
  },
});

let app: App | undefined;
let container: HTMLDivElement;
let pinia: Pinia;

function mountDemo() {
  container = document.createElement('div');
  document.body.append(container);
  app = createApp(Demo);
  app.use(pinia);
  registerAccessDirective(app);
  app.mount(container);
  return container.querySelector('button') as HTMLButtonElement;
}

function setUserCodes(userCodes: string[]) {
  useAccessStore().accessCodes = userCodes;
}

/** 触发组件重建，让 v-access 的 updated 钩子重新求值。 */
async function rerender() {
  codes.value = [...codes.value];
  await nextTick();
}

beforeEach(() => {
  pinia = createPinia();
  setActivePinia(pinia);
  codes.value = ['iam:role:write'];
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  container?.remove();
});

describe('v-access 指令', () => {
  it('无权限时在 mounted 阶段隐藏元素', () => {
    setUserCodes([]);
    const button = mountDemo();
    expect(button.style.display).toBe('none');
  });

  it('权限快照刷新后 updated 恢复显示', async () => {
    setUserCodes([]);
    const button = mountDemo();
    expect(button.style.display).toBe('none');

    setUserCodes(['iam:role:write']);
    await rerender();
    expect(button.style.display).toBe('');
  });

  it('权限被回收后 updated 重新隐藏', async () => {
    setUserCodes(['iam:role:write']);
    const button = mountDemo();
    expect(button.style.display).toBe('');

    setUserCodes([]);
    await rerender();
    expect(button.style.display).toBe('none');
  });
});

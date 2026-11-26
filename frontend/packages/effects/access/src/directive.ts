/**
 * Global authority directive
 * Used for fine-grained control of component permissions
 * @Example v-access:role="[ROLE_NAME]" or v-access:role="ROLE_NAME"
 * @Example v-access:code="['iam:role:write', 'iam:role:grant']"   任一命中即显示（并集）
 * @Example v-access:code.all="['iam:role:write', 'iam:role:grant']" 全部命中才显示（交集）
 *
 * 仅做展示控制，不是安全边界：隐藏按钮不阻止直接调接口，服务端必须用
 * `perm()` 兜底（见 docs/FRONTEND_STANDARDS.md §4）。
 *
 * 不满足时通过 `display: none` 隐藏而非移除 DOM，这样权限快照在登录后
 * 异步刷新（mounted 之后才拿到 accessCodes）时 `updated` 能即时恢复显示。
 */
import type { App, Directive, DirectiveBinding } from 'vue';

import { useAccess } from './use-access';

type AccessBinding = DirectiveBinding<string | string[]>;

const originalDisplay = new WeakMap<Element, string>();

function applyAccess(el: Element, binding: AccessBinding) {
  const {
    accessMode,
    hasAccessByCodes,
    hasAllAccessByCodes,
    hasAccessByRoles,
  } = useAccess();

  const value = binding.value;
  let allowed = true;

  if (value) {
    let authMethod = hasAccessByCodes;
    if (accessMode.value === 'frontend' && binding.arg === 'role') {
      authMethod = hasAccessByRoles;
    } else if (binding.modifiers.all) {
      authMethod = hasAllAccessByCodes;
    }

    const values = Array.isArray(value) ? value : [value];
    allowed = authMethod(values);
  }

  if (!originalDisplay.has(el)) {
    originalDisplay.set(el, (el as HTMLElement).style.display);
  }
  const style = (el as HTMLElement).style;
  style.display = allowed ? (originalDisplay.get(el) ?? '') : 'none';
}

const mounted = (el: Element, binding: AccessBinding) => {
  applyAccess(el, binding);
};

const updated = (el: Element, binding: AccessBinding) => {
  applyAccess(el, binding);
};

const authDirective: Directive = {
  mounted,
  updated,
};

export function registerAccessDirective(app: App) {
  app.directive('access', authDirective);
}

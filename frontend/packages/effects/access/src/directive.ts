/**
 * Global authority directive
 * Used for fine-grained control of component permissions
 * @Example v-access:role="[ROLE_NAME]" or v-access:role="ROLE_NAME"
 * @Example v-access:code="['iam:role:write', 'iam:role:grant']"   任一命中即显示（并集）
 * @Example v-access:code.all="['iam:role:write', 'iam:role:grant']" 全部命中才显示（交集）
 */
import type { App, Directive, DirectiveBinding } from 'vue';

import { useAccess } from './use-access';

function isAccessible(
  el: Element,
  binding: DirectiveBinding<string | string[]>,
) {
  const {
    accessMode,
    hasAccessByCodes,
    hasAccessByRoles,
    hasAllAccessByCodes,
  } = useAccess();

  const value = binding.value;

  if (!value) return;
  let authMethod = hasAccessByCodes;
  if (accessMode.value === 'frontend' && binding.arg === 'role') {
    authMethod = hasAccessByRoles;
  } else if (binding.modifiers.all) {
    authMethod = hasAllAccessByCodes;
  }

  const values = Array.isArray(value) ? value : [value];

  if (!authMethod(values)) {
    el?.remove();
  }
}

const mounted = (el: Element, binding: DirectiveBinding<string | string[]>) => {
  isAccessible(el, binding);
};

const authDirective: Directive = {
  mounted,
};

export function registerAccessDirective(app: App) {
  app.directive('access', authDirective);
}

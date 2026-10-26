import { defaultTranslator, type MessageKey } from '@admin-frame/i18n';
import { useEffect } from 'react';
import { useBlocker } from 'react-router';

export type UnsavedChangesGuard = {
  /** 路由跳转是否已被拦截；调用方应据此渲染确认弹窗。 */
  blocked: boolean;
  /** 用户确认离开：放行本次被拦截的跳转。 */
  proceed: () => void;
  /** 用户取消离开：留在当前页并解除拦截。 */
  reset: () => void;
};

/**
 * 未保存修改离开提示（S3 / INF-UI-04）。
 *
 * 用法：
 * ```tsx
 * const guard = useUnsavedChangesGuard(isDirty);
 * <ConfirmDialog
 *   open={guard.blocked}
 *   title={t('common.unsavedChanges')}
 *   content={null}
 *   onConfirm={guard.proceed}
 *   onCancel={guard.reset}
 * />;
 * ```
 *
 * - 站内跳转由 `useBlocker` 拦截，确认弹窗由调用方渲染，避免设计系统依赖路由；
 * - 关闭标签/刷新只能使用原生 `beforeunload`，文案由浏览器决定，返回值仅供诊断；
 * - 本 hook 只提供能力，页面是否启用由调用方决定（本轮不改造登录页）。
 */
export function useUnsavedChangesGuard(
  when: boolean,
  options: { messageKey?: MessageKey } = {},
): UnsavedChangesGuard {
  const messageKey = options.messageKey ?? 'common.unsavedChanges';
  const blocker = useBlocker(when);

  useEffect(() => {
    if (!when) {
      return;
    }
    const handleBeforeUnload = (event: BeforeUnloadEvent): void => {
      event.preventDefault();
      event.returnValue = defaultTranslator(messageKey);
    };
    window.addEventListener('beforeunload', handleBeforeUnload);
    return () => {
      window.removeEventListener('beforeunload', handleBeforeUnload);
    };
  }, [when, messageKey]);

  return {
    blocked: blocker.state === 'blocked',
    proceed: () => {
      if (blocker.state === 'blocked') {
        blocker.proceed();
      }
    },
    reset: () => {
      if (blocker.state === 'blocked') {
        blocker.reset();
      }
    },
  };
}

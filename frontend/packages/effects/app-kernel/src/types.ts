/**
 * app-kernel 能力开关（ADR-0019 / FE-K-02 裁决）。
 *
 * 关闭的能力保留方法入口，但调用即抛 KernelCapabilityError，
 * 避免把未启用的流程静默当作成功（fail-closed）。
 */
export interface KernelCapabilities {
  /** MFA 两步校验（challenge / verify）。 */
  mfa: boolean;
  /** MFA 强制绑定。 */
  mfaBinding: boolean;
  /** 强制改密。 */
  passwordChange: boolean;
  /** 写请求幂等头。 */
  idempotency: boolean;
  /** 401 自动刷新令牌。 */
  refresh: boolean;
}

export class KernelCapabilityError extends Error {
  readonly capability: keyof KernelCapabilities;

  constructor(capability: keyof KernelCapabilities) {
    super(`Kernel capability disabled: ${capability}`);
    this.name = 'KernelCapabilityError';
    this.capability = capability;
  }
}

/** A 端：全部能力开启。 */
export const ADMIN_CAPABILITIES: KernelCapabilities = {
  mfa: true,
  mfaBinding: true,
  passwordChange: true,
  idempotency: true,
  refresh: true,
};

/** B 端：仅保留刷新（登录无 MFA/改密中间态、无幂等头模块）。 */
export const BIZ_CAPABILITIES: KernelCapabilities = {
  mfa: false,
  mfaBinding: false,
  passwordChange: false,
  idempotency: false,
  refresh: true,
};

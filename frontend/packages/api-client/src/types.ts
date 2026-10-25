import type { components as AdminComponents } from './generated/admin';
import type { components as BizComponents } from './generated/biz';

export type TokenPair = AdminComponents['schemas']['TokenPair'];
export type MFAChallenge = AdminComponents['schemas']['MFAChallenge'];
export type MFABindingRequired = AdminComponents['schemas']['MFABindingRequired'];
export type LoginRequest = AdminComponents['schemas']['LoginRequest'];
export type SubjectPermissions = AdminComponents['schemas']['SubjectPermissions'];
export type BizSubjectPermissions = BizComponents['schemas']['SubjectPermissions'];
export type MFASetupData = AdminComponents['schemas']['MFASetupData'];
export type AuthSession = AdminComponents['schemas']['AuthSession'];
export type AuthSessionListData = AdminComponents['schemas']['AuthSessionListData'];

/**
 * 登录响应是 `TokenPair | MFAChallenge | MFABindingRequired`，必须显式窄化（§10.1）。
 *
 * `mfaSetup` 表示密码已通过但账号尚未绑定 MFA：先用 `mfa_setup_ticket` 初始化绑定，
 * 再用同一票据提交首码，成功即获得令牌（§7.2.1）。
 */
export type LoginOutcome =
  | { kind: 'tokens'; tokens: TokenPair }
  | { kind: 'mfa'; challenge: MFAChallenge }
  | { kind: 'mfaSetup'; challenge: MFABindingRequired };

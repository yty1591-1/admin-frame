import type { components as AdminComponents } from './generated/admin';
import type { components as BizComponents } from './generated/biz';

export type TokenPair = AdminComponents['schemas']['TokenPair'];
export type MFAChallenge = AdminComponents['schemas']['MFAChallenge'];
export type LoginRequest = AdminComponents['schemas']['LoginRequest'];
export type SubjectPermissions = AdminComponents['schemas']['SubjectPermissions'];
export type BizSubjectPermissions = BizComponents['schemas']['SubjectPermissions'];

/** 登录响应是 `TokenPair | MFAChallenge`，必须显式窄化（§10.1）。 */
export type LoginOutcome =
  { kind: 'tokens'; tokens: TokenPair } | { kind: 'mfa'; challenge: MFAChallenge };

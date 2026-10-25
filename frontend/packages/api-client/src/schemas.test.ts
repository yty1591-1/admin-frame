import { describe, expect, it } from 'vitest';
import { loginOutcomeSchema, tokenPairSchema } from './schemas';

/**
 * 登录 200 的三种中间态（§10.1、§7.2.1）：
 * 令牌对 / 两步登录挑战 / 强制绑定挑战，必须能结构化窄化，不依赖 message 文案。
 */
describe('loginOutcomeSchema', () => {
  const tokens = {
    access_token: 'access-1',
    refresh_token: 'refresh-1',
    token_type: 'Bearer' as const,
    expires_in: 600,
  };

  it('令牌对窄化为 tokens', () => {
    expect(loginOutcomeSchema.parse(tokens)).toEqual({ kind: 'tokens', tokens });
  });

  it('两步登录窄化为 mfa', () => {
    const challenge = { mfa_required: true, mfa_ticket: 'ticket-1' };
    expect(loginOutcomeSchema.parse(challenge)).toEqual({ kind: 'mfa', challenge });
  });

  it('强制绑定窄化为 mfaSetup', () => {
    const challenge = { mfa_binding_required: true, mfa_setup_ticket: 'setup-1' };
    expect(loginOutcomeSchema.parse(challenge)).toEqual({ kind: 'mfaSetup', challenge });
  });

  it('缺少绑定票据时拒绝，避免前端进入无票据流程', () => {
    expect(() => loginOutcomeSchema.parse({ mfa_binding_required: true })).toThrow();
    expect(() => loginOutcomeSchema.parse({ mfa_binding_required: false })).toThrow();
  });

  it('令牌对拒绝未知畸形字段', () => {
    expect(() => tokenPairSchema.parse({ ...tokens, expires_in: 0 })).toThrow();
  });
});

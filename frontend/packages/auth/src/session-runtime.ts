import type { AdminAuthApi, MFASetupData, TokenPair } from '@admin-frame/api-client';
import { AppError } from '@admin-frame/api-client';
import type { RefreshManager } from './refresh-manager';
import type { TokenStore } from './token-store';

export type LoginResult =
  | { kind: 'authenticated' }
  | { kind: 'mfa'; ticket: string }
  | { kind: 'mfaSetup'; ticket: string };

export type SessionRuntimeOptions = {
  auth: Pick<
    AdminAuthApi,
    'login' | 'verifyMfaLogin' | 'verifyMfaSetup' | 'setupMfa' | 'logout' | 'logoutAll'
  >;
  store: TokenStore;
  refreshManager: Pick<RefreshManager, 'refreshAccessToken'>;
};

/**
 * 登录态编排（§10.1～§10.3）：登录、两步 MFA、强制绑定、启动期恢复与登出。
 * 票据只驻留内存，一次性消费后立即清空，禁止写入存储与日志。
 */
export class SessionRuntime {
  private readonly options: SessionRuntimeOptions;
  private mfaTicket: string | null = null;
  private mfaSetupTicket: string | null = null;
  private restorePromise: Promise<boolean> | null = null;

  constructor(options: SessionRuntimeOptions) {
    this.options = options;
  }

  async login(subject: string, password: string): Promise<LoginResult> {
    const outcome = await this.options.auth.login(subject, password);
    if (outcome.kind === 'mfa') {
      this.mfaTicket = outcome.challenge.mfa_ticket;
      return { kind: 'mfa', ticket: outcome.challenge.mfa_ticket };
    }
    if (outcome.kind === 'mfaSetup') {
      this.mfaSetupTicket = outcome.challenge.mfa_setup_ticket;
      return { kind: 'mfaSetup', ticket: outcome.challenge.mfa_setup_ticket };
    }
    this.acceptTokens(outcome.tokens);
    return { kind: 'authenticated' };
  }

  /** 两步登录；票据一次性消费，动态码错误同样视为票据失效。 */
  async verifyMfa(code: string): Promise<void> {
    const ticket = this.mfaTicket;
    if (!ticket) {
      throw new AppError({
        kind: 'validation',
        code: 40000,
        message: 'MFA 登录票据已失效，请重新登录',
      });
    }
    try {
      this.acceptTokens(await this.options.auth.verifyMfaLogin(ticket, code));
    } finally {
      this.mfaTicket = null;
    }
  }

  cancelMfa(): void {
    this.mfaTicket = null;
  }

  hasMfaChallenge(): boolean {
    return this.mfaTicket !== null;
  }

  /** 登录前强制绑定第一步：用绑定票据初始化 TOTP 密钥与恢复码（§7.2.1）。 */
  async beginMfaSetup(): Promise<MFASetupData> {
    const ticket = this.mfaSetupTicket;
    if (!ticket) {
      throw new AppError({
        kind: 'validation',
        code: 40000,
        message: 'MFA 绑定票据已失效，请重新登录',
      });
    }
    return await this.options.auth.setupMfa(ticket);
  }

  /** 强制绑定第二步：提交首码换取令牌；票据一次性消费，失败后须重新登录。 */
  async confirmMfaSetup(code: string): Promise<void> {
    const ticket = this.mfaSetupTicket;
    if (!ticket) {
      throw new AppError({
        kind: 'validation',
        code: 40000,
        message: 'MFA 绑定票据已失效，请重新登录',
      });
    }
    try {
      this.acceptTokens(await this.options.auth.verifyMfaSetup(ticket, code));
    } finally {
      this.mfaSetupTicket = null;
    }
  }

  cancelMfaSetup(): void {
    this.mfaSetupTicket = null;
  }

  hasMfaSetupChallenge(): boolean {
    return this.mfaSetupTicket !== null;
  }

  /**
   * 启动期恢复：刷新令牌换取新的访问令牌。
   * 结果缓存以避免 StrictMode 双挂载触发两次轮换（重放会导致整链撤销）。
   */
  restore(): Promise<boolean> {
    if (!this.restorePromise) {
      this.restorePromise = this.performRestore();
    }
    return this.restorePromise;
  }

  async logout(): Promise<void> {
    try {
      await this.options.auth.logout();
    } finally {
      this.loseSession();
    }
  }

  async logoutAll(): Promise<void> {
    try {
      await this.options.auth.logoutAll();
    } finally {
      this.loseSession();
    }
  }

  private async performRestore(): Promise<boolean> {
    if (!this.options.store.hasSession()) {
      return false;
    }
    try {
      await this.options.refreshManager.refreshAccessToken();
      return true;
    } catch {
      return false;
    }
  }

  private acceptTokens(tokens: TokenPair): void {
    this.options.store.setTokens(tokens);
    this.restorePromise = Promise.resolve(true);
  }

  private loseSession(): void {
    this.mfaTicket = null;
    this.mfaSetupTicket = null;
    this.options.store.clear();
    this.restorePromise = Promise.resolve(false);
  }
}

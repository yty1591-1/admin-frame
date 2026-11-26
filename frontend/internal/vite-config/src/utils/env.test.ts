import { describe, expect, it } from 'vitest';

import { assertDeclaredEnv } from './env';

describe('assertDeclaredEnv', () => {
  it('accepts declared .env keys', () => {
    expect(() =>
      assertDeclaredEnv(['VITE_APP_TITLE', 'VITE_GLOB_API_URL']),
    ).not.toThrow();
  });

  it('accepts injected build metadata keys', () => {
    expect(() =>
      assertDeclaredEnv([
        'VITE_APP_BUILD_TIME',
        'VITE_APP_GIT_SHA',
        'VITE_APP_VERSION',
      ]),
    ).not.toThrow();
  });

  it('rejects undeclared keys and names every offender', () => {
    expect(() =>
      assertDeclaredEnv(['VITE_APP_TITLE', 'VITE_BOGUS_FLAG', 'VITE_LEAK_KEY']),
    ).toThrow(/VITE_BOGUS_FLAG, VITE_LEAK_KEY/);
  });

  it('mentions the allowlist location for remediation', () => {
    expect(() => assertDeclaredEnv(['VITE_UNKNOWN_FLAG'])).toThrow(
      /DECLARED_ENV_KEYS/,
    );
  });
});

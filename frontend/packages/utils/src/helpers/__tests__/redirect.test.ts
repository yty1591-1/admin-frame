import { describe, expect, it } from 'vitest';

import { resolveSafeRedirect } from '../redirect';

const FALLBACK = '/home';

describe('resolveSafeRedirect', () => {
  it('保留站内相对路径', () => {
    expect(resolveSafeRedirect('/system/user', FALLBACK)).toBe('/system/user');
  });

  it('解码一次并保留 query 与 hash', () => {
    expect(
      resolveSafeRedirect('%2Fsystem%2Fuser%3Ftab%3D1%23section', FALLBACK),
    ).toBe('/system/user?tab=1#section');
  });

  it('畸形转义退回兜底而不抛错', () => {
    expect(resolveSafeRedirect('%E0%A4%A', FALLBACK)).toBe(FALLBACK);
    expect(resolveSafeRedirect('%', FALLBACK)).toBe(FALLBACK);
  });

  it('拒绝协议相对地址', () => {
    expect(resolveSafeRedirect('//evil.example/x', FALLBACK)).toBe(FALLBACK);
  });

  it('拒绝绝对 URL 与伪协议', () => {
    expect(resolveSafeRedirect('https://evil.example/x', FALLBACK)).toBe(
      FALLBACK,
    );
    expect(resolveSafeRedirect('javascript:alert(1)', FALLBACK)).toBe(FALLBACK);
  });

  it('拒绝反斜杠与控制字符', () => {
    expect(resolveSafeRedirect(String.raw`/\evil.example`, FALLBACK)).toBe(
      FALLBACK,
    );
    expect(resolveSafeRedirect('/a%0Ab', FALLBACK)).toBe(FALLBACK);
  });

  it('拒绝双次编码', () => {
    expect(resolveSafeRedirect('%252F%252Fevil', FALLBACK)).toBe(FALLBACK);
  });

  it('数组取第一个；缺失/空白退回兜底', () => {
    expect(resolveSafeRedirect(['/a', '/b'], FALLBACK)).toBe('/a');
    expect(resolveSafeRedirect(undefined, FALLBACK)).toBe(FALLBACK);
    expect(resolveSafeRedirect('   ', FALLBACK)).toBe(FALLBACK);
  });

  it('同源内规范化点段', () => {
    expect(resolveSafeRedirect('/a/../b', FALLBACK)).toBe('/b');
  });
});

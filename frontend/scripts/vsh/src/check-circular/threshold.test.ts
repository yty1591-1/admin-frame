import { describe, expect, it } from 'vitest';

import { exceedsThreshold } from './threshold';

describe('exceedsThreshold', () => {
  it('无循环依赖时不判失败', () => {
    expect(exceedsThreshold(0, 0)).toBe(false);
  });

  it('数量超过阈值时判失败', () => {
    expect(exceedsThreshold(1, 0)).toBe(true);
  });

  it('数量等于阈值时不判失败', () => {
    expect(exceedsThreshold(2, 2)).toBe(false);
  });
});

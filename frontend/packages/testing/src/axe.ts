import axe from 'axe-core';

export type A11yViolation = {
  id: string;
  impact: string | null;
  help: string;
  /** 命中节点选择器，便于定位。 */
  nodes: readonly string[];
};

const GATED_IMPACTS = ['serious', 'critical'];

/**
 * 可访问性断言（INF-TEST-05）：只返回 serious/critical 违规，测试以 `toEqual([])` 断言。
 * 颜色对比度在 jsdom 无布局/画布时无法判定，交由 E2E + Lighthouse 覆盖（§11.3）。
 */
export async function findSeriousA11yViolations(container: Element): Promise<A11yViolation[]> {
  const results = await axe.run(container, {
    rules: {
      'color-contrast': { enabled: false },
    },
  });
  return results.violations
    .filter((violation) => GATED_IMPACTS.includes(violation.impact ?? ''))
    .map((violation) => ({
      id: violation.id,
      impact: violation.impact ?? null,
      help: violation.help,
      nodes: violation.nodes.map((node) => node.target.join(' ')),
    }));
}

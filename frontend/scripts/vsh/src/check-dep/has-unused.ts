interface KnipDependency {
  col: number;
  line: number;
  name: string;
  pos: number;
}

interface KnipFileIssue {
  dependencies: KnipDependency[];
  devDependencies: KnipDependency[];
  file: string;
  optionalPeerDependencies: KnipDependency[];
}

interface KnipResult {
  issues: KnipFileIssue[];
}

/**
 * 判断 knip 结果中是否存在未使用的依赖。
 * 检查脚本必须以此判定退出码，否则门禁恒为绿。
 * @param result - knip 输出的问题列表
 * @returns 存在未使用依赖时为 true
 */
function hasUnusedDependencies(result: KnipResult): boolean {
  return result.issues.some(
    (issue) =>
      issue.dependencies.length > 0 || issue.devDependencies.length > 0,
  );
}

export { hasUnusedDependencies };
export type { KnipFileIssue, KnipResult };

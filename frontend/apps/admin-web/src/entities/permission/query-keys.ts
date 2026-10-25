/** 权限快照 Query Key（§8.2）；端别固定为 admin，禁止页面自行拼接字符串。 */
export const permissionSnapshotKeys = {
  current: ['admin', 'iam', 'me', 'permissions'] as const,
};

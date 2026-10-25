import type { AdminIamApi } from '@admin-frame/api-client';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { permissionSnapshotKeys } from '../../entities/permission';
import { invalidateAfterWrite } from '../../shared';
import { iamRoleKeys } from './query-keys';

export type DeleteRoleVariables = {
  id: string;
  reason: string;
  /** 同一次提交的重试必须复用同一个 key（§7.3）。 */
  idempotencyKey: string;
};

/**
 * 删除角色：成功后只失效角色列表与权限快照（§8.2），
 * 40900 由调用方转审批引导，不做自动重试。
 */
export function useDeleteRole(api: AdminIamApi) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (variables: DeleteRoleVariables) =>
      api.deleteRole(variables.id, {
        reason: variables.reason,
        idempotencyKey: variables.idempotencyKey,
      }),
    onSuccess: async () => {
      await invalidateAfterWrite(queryClient, [iamRoleKeys.all, permissionSnapshotKeys.current]);
    },
  });
}

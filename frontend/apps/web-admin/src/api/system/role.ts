import { requestClient } from '#/api/request';

export namespace RoleApi {
  /** 角色列表项 */
  export interface RoleItem {
    id: string;
    name: string;
    status: 0 | 1;
    permissions?: string[];
    remark?: string;
    createTime: string;
  }

  /** 列表查询参数 */
  export interface ListParams {
    page: number;
    pageSize: number;
    name?: string;
    status?: 0 | 1;
  }

  /** 分页列表响应 */
  export interface ListResult {
    items: RoleItem[];
    total: number;
  }
}

/**
 * 查询角色列表
 *
 * 当前对接 Nitro Mock：GET /system/role/list，
 * 字段为 name / status(0|1) / remark / createTime。
 *
 * 切换 admin-frame 后端时（GET /admin/v1/iam/roles）需要改三处：
 * 1. 端点：改为 `/iam/roles`，并把 VITE_GLOB_API_URL 指向 /admin/v1；
 * 2. 分页参数：后端为 page / page_size（上限 100），过滤参数以后端 DTO 为准；
 * 3. 字段映射：后端 RoleDTO 为 code / name / status(字符串枚举) / description / created_at，
 *    需要把表格列与筛选条件从 remark、createTime、0|1 状态改为上述字段。
 * 响应结构无需改动：后端同样返回 { items, total }。
 */
export async function getRoleListApi(params: RoleApi.ListParams) {
  return requestClient.get<RoleApi.ListResult>('/system/role/list', { params });
}

import { requestClient } from '#/api/request';

export namespace MemberApi {
  /** 成员列表项 */
  export interface MemberItem {
    id: string;
    name: string;
    status: 0 | 1;
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
    items: MemberItem[];
    total: number;
  }
}

/**
 * 查询成员列表
 *
 * 当前对接 Nitro Mock：GET /system/user/list，
 * 字段为 name / status(0|1) / remark / createTime。
 *
 * admin-frame 后端暂无「商户成员列表」端点，接入时按实际契约替换：
 * - B 端路由统一为 /biz/v1/... 前缀（例如成员与角色绑定可先用
 *   GET /biz/v1/iam/bindings）；
 * - 分页参数为 page / page_size（上限 100）；
 * - 保持返回 { items, total }，前端调用方无需改动。
 */
export async function getMemberListApi(params: MemberApi.ListParams) {
  return requestClient.get<MemberApi.ListResult>('/system/user/list', {
    params,
  });
}

import { requestClient } from '#/api/request';

export namespace MemberApi {
  /** 成员列表项 */
  export interface MemberItem {
    createTime: string;
    id: string;
    name: string;
    remark?: string;
    status: 0 | 1;
  }

  /** 列表查询参数 */
  export interface ListParams {
    name?: string;
    page: number;
    pageSize: number;
    status?: 0 | 1;
  }

  /** 分页列表响应 */
  export interface ListResult {
    items: MemberItem[];
    total: number;
  }
}

/**
 * 查询成员列表（示例模块，可作为其他业务模块的模板）。
 *
 * **当前后端未提供该端点**：admin-frame 只有 `GET /biz/v1/iam/bindings`
 * （角色绑定，不含成员档案），因此本请求会返回 404，页面已做空表降级。
 * 待后端补齐商户成员端点后按实际契约替换即可，前端调用方无需改动：
 * - 路径统一为 `/biz/v1/...` 前缀（baseURL 已包含端别前缀，此处只写相对路径）；
 * - 分页参数为 `page` / `page_size`（上限 100）；
 * - 返回结构保持 `{ items, total }`。
 */
export async function getMemberListApi(params: MemberApi.ListParams) {
  return requestClient.get<MemberApi.ListResult>('/system/user/list', {
    params,
  });
}

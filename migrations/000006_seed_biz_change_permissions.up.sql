-- 000006 追加 B 端变更审批的权限点（只追加，不改写 000003）。
--
-- 背景：B 端审批单随迁移 000005（merchant_id 租户隔离）开放。为了做到纵深防御，
-- B 端不复用 A 端的 iam:change:read / iam:change:approve，而是新增两个 biz 权限点：
--   * iam:change:request：发起本商户申请 + 查看本商户待办列表；
--   * iam:change:review ：审批本商户申请（与申请人强制分离）。
-- 这样即使商户过滤逻辑出现缺陷，持有 B 端权限点的主体也没有平台级审批单的权限。
--
-- 约定与 000003 一致：每条权限点独占一行、行首为 "('"，供 internal/iam/catalog
-- 的一致性测试解析；时间列取数据库 UTC，避免迁移机器本地时区污染。

INSERT INTO iam_permission
    (code, name, channel, resource, action, kind, data_bearing, parent_code, path, sort, status, created_at, updated_at)
VALUES
    ('iam:change:request', '发起变更申请', 'biz', 'iam', 'request', 'api', 0, '', '', 200, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:change:review', '审批本商户变更', 'biz', 'iam', 'review', 'api', 0, '', '', 210, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
AS new
ON DUPLICATE KEY UPDATE
    name         = new.name,
    channel      = new.channel,
    resource     = new.resource,
    action       = new.action,
    kind         = new.kind,
    data_bearing = new.data_bearing,
    parent_code  = new.parent_code,
    path         = new.path,
    sort         = new.sort,
    status       = new.status,
    updated_at   = UTC_TIMESTAMP(3);

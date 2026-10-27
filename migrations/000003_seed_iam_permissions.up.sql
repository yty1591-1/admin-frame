-- IAM 权限点目录种子（与 internal/iam/catalog、internal/iam/perm.go、docs/iam/PERMISSIONS.md 四处一致）。
-- 本文件是模块基线种子：合并原 A 端目录、B 端变更审批权限点与演示域弃用状态。
-- 幂等：重复执行只更新元数据，不删除既有授权；新增权限点只能追加，禁止改写既有 code 语义。
-- 每条权限点独占一行，行首为 "('"，供 internal/iam/catalog 的单元测试做一致性解析。
-- 时间列由数据库取 UTC，避免迁移机器本地时区污染。
--
-- 约定：
--   * channel：admin=仅 A 端可授予 / biz=仅 B 端可授予 / both=两端均可；
--   * order:* / file:* 属于已移除的演示域，保留码值以解释历史审计，status 固定 deprecated；
--   * iam:change:request / iam:change:review 为 B 端专属，A 端审批仍用 iam:change:read / approve。

INSERT INTO iam_permission
    (code, name, channel, resource, action, kind, data_bearing, parent_code, path, sort, status, created_at, updated_at)
VALUES
    ('iam:permission:read', '查看权限目录', 'admin', 'iam', 'read', 'api', 0, NULL, '', 10, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:role:read', '查看角色', 'both', 'iam', 'read', 'api', 0, NULL, '', 20, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:role:write', '管理角色', 'both', 'iam', 'write', 'api', 0, NULL, '', 30, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:role:grant', '授予角色权限', 'both', 'iam', 'grant', 'api', 0, NULL, '', 40, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:binding:read', '查看绑定', 'both', 'iam', 'read', 'api', 0, NULL, '', 50, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:binding:write', '管理绑定', 'both', 'iam', 'write', 'api', 0, NULL, '', 60, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:check', '模拟鉴权', 'admin', 'iam', 'check', 'api', 0, NULL, '', 70, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:change:read', '查看审批单', 'admin', 'iam', 'read', 'api', 0, NULL, '', 80, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:change:approve', '审批变更', 'admin', 'iam', 'approve', 'api', 0, NULL, '', 90, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:breakglass:request', '申请破玻璃', 'admin', 'iam', 'request', 'api', 0, NULL, '', 100, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:breakglass:approve', '审批破玻璃', 'admin', 'iam', 'approve', 'api', 0, NULL, '', 110, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('order:read', '查询订单', 'both', 'order', 'read', 'api', 1, NULL, '', 120, 'deprecated', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('order:write', '变更订单', 'both', 'order', 'write', 'api', 1, NULL, '', 130, 'deprecated', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('file:read', '查看文件', 'both', 'file', 'read', 'api', 1, NULL, '', 140, 'deprecated', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('file:write', '管理文件', 'both', 'file', 'write', 'api', 1, NULL, '', 150, 'deprecated', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('audit:read', '查询审计', 'admin', 'audit', 'read', 'api', 1, NULL, '', 160, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('data:export', '导出数据', 'both', 'data', 'export', 'api', 1, NULL, '', 170, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('config:read', '查看配置', 'admin', 'config', 'read', 'api', 0, NULL, '', 180, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('config:write', '修改配置', 'admin', 'config', 'write', 'api', 0, NULL, '', 190, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:change:request', '发起变更申请', 'biz', 'iam', 'request', 'api', 0, NULL, '', 200, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:change:review', '审批本商户变更', 'biz', 'iam', 'review', 'api', 0, NULL, '', 210, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
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

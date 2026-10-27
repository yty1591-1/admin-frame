-- IAM 权限点目录种子（与 internal/iam/catalog、internal/iam/perm.go、docs/iam/PERMISSIONS.md 四处一致）。
-- 本文件是模块基线种子：合并原 A 端目录、B 端变更审批权限点与演示域弃用状态。
-- 幂等：重复执行只更新元数据，不删除既有授权；新增权限点只能追加，禁止改写既有 code 语义。
-- 每条权限点独占一行，行首为 "('"，供 internal/iam/catalog 的单元测试做一致性解析。
-- 时间列由数据库取 UTC，避免迁移机器本地时区污染。
--
-- 约定：
--   * channel：admin=仅 A 端可授予 / biz=仅 B 端可授予 / both=两端均可；
--   * resource 是生成列（由 code 首段派生），本文件不得写入；
--   * order:* / file:* 属于已移除的演示域，保留码值以解释历史审计，status 固定 deprecated；
--   * iam:change:request / iam:change:review 为 B 端专属，A 端审批仍用 iam:change:read / approve。

INSERT INTO iam_permission
    (code, name, channel, data_bearing, sort, status, created_at, updated_at)
VALUES
    ('iam:permission:read', '查看权限目录', 'admin', 0, 10, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:role:read', '查看角色', 'both', 0, 20, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:role:write', '管理角色', 'both', 0, 30, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:role:grant', '授予角色权限', 'both', 0, 40, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:binding:read', '查看绑定', 'both', 0, 50, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:binding:write', '管理绑定', 'both', 0, 60, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:check', '模拟鉴权', 'admin', 0, 70, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:change:read', '查看审批单', 'admin', 0, 80, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:change:approve', '审批变更', 'admin', 0, 90, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:breakglass:request', '申请破玻璃', 'admin', 0, 100, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:breakglass:approve', '审批破玻璃', 'admin', 0, 110, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('order:read', '查询订单', 'both', 1, 120, 'deprecated', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('order:write', '变更订单', 'both', 1, 130, 'deprecated', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('file:read', '查看文件', 'both', 1, 140, 'deprecated', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('file:write', '管理文件', 'both', 1, 150, 'deprecated', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('audit:read', '查询审计', 'admin', 1, 160, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('data:export', '导出数据', 'both', 1, 170, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('config:read', '查看配置', 'admin', 0, 180, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('config:write', '修改配置', 'admin', 0, 190, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:change:request', '发起变更申请', 'biz', 0, 200, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)),
    ('iam:change:review', '审批本商户变更', 'biz', 0, 210, 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
AS new
ON DUPLICATE KEY UPDATE
    name         = new.name,
    channel      = new.channel,
    data_bearing = new.data_bearing,
    sort         = new.sort,
    status       = new.status,
    updated_at   = UTC_TIMESTAMP(3);

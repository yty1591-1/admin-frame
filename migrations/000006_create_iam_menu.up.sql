-- 菜单 / 路由资源表。
-- 权限目录（iam_permission）只描述「能做哪些操作」，菜单描述「前端可见哪些入口」；
-- 两者通过 permission_code 关联，但授权关系仍只存于 iam_role_permission。
-- 设计要点：
--   1. 菜单按端别隔离：A 端（admin）与 B 端（biz）的菜单树互不合并；
--   2. (channel, code) 与 (channel, route_name) 唯一：vue-router 同名路由会互相覆盖；
--   3. 自引用外键保证父子节点同端，避免跨端挂载；
--   4. 菜单由代码种子维护，接口只读，不提供后台 CRUD。

CREATE TABLE IF NOT EXISTS iam_menu (
    id              BIGINT       NOT NULL                  COMMENT '菜单主键，雪花 ID 或种子保留段固定值',
    channel         VARCHAR(16)  NOT NULL                  COMMENT '归属端别：admin=web-admin / biz=web-biz；两端菜单不合并',
    code            VARCHAR(128) NOT NULL                  COMMENT '菜单稳定标识，如 system.role；父子引用与种子幂等键',
    parent_code     VARCHAR(128) NULL DEFAULT NULL         COMMENT '父菜单 code；根节点为 NULL',
    node_type       VARCHAR(16)  NOT NULL DEFAULT 'menu'   COMMENT 'catalog=目录 / menu=页面 / iframe=内嵌 / link=外链',
    route_name      VARCHAR(64)  NOT NULL                  COMMENT '路由 name，PascalCase，端内唯一；keep-alive 与标签页依赖',
    route_path      VARCHAR(256) NOT NULL                  COMMENT '路由 URL：目录为 /system，子页可为相对段 role',
    component       VARCHAR(256) NOT NULL DEFAULT ''       COMMENT 'BasicLayout / IFrameView / /system/role/index；catalog 与 link 为空',
    redirect        VARCHAR(256) NOT NULL DEFAULT ''       COMMENT '目录默认跳转，catalog 必填',
    title           VARCHAR(128) NOT NULL                  COMMENT '显示名；i18n 缺失时兜底',
    title_key       VARCHAR(128) NOT NULL DEFAULT ''       COMMENT 'i18n key，如 page.system.role；非空时前端优先',
    icon            VARCHAR(64)  NOT NULL DEFAULT ''       COMMENT '图标注册表 key，如 lucide:settings',
    permission_code VARCHAR(128) NULL DEFAULT NULL         COMMENT '可见所需权限点；NULL=登录即可见',
    sort            INT          NOT NULL DEFAULT 0        COMMENT '同级升序，映射 meta.order',
    hide_in_menu    TINYINT      NOT NULL DEFAULT 0        COMMENT '1=路由可达但不进侧边栏',
    keep_alive      TINYINT      NOT NULL DEFAULT 0        COMMENT '1=页面缓存',
    external_url    VARCHAR(512) NOT NULL DEFAULT ''       COMMENT 'iframe / link 目标地址',
    status          VARCHAR(16)  NOT NULL DEFAULT 'active' COMMENT 'active / disabled',
    updated_by      VARCHAR(64)  NOT NULL                  COMMENT '最后维护人；种子写入 seed',
    created_at      DATETIME(3)  NOT NULL                  COMMENT '创建时间（UTC）',
    updated_at      DATETIME(3)  NOT NULL                  COMMENT '更新时间（UTC）',
    PRIMARY KEY (id),
    UNIQUE KEY uk_iam_menu_code (channel, code) COMMENT '端内菜单编码唯一',
    UNIQUE KEY uk_iam_menu_route_name (channel, route_name) COMMENT 'vue-router 同名路由会互相覆盖',
    KEY idx_iam_menu_tree (channel, status, parent_code, sort) COMMENT '菜单树按端别 + 状态 + 父节点排序输出',
    KEY idx_iam_menu_permission (permission_code) COMMENT '按权限点反查受影响菜单',
    CONSTRAINT fk_iam_menu_parent FOREIGN KEY (channel, parent_code) REFERENCES iam_menu (channel, code)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT fk_iam_menu_permission FOREIGN KEY (permission_code) REFERENCES iam_permission (code)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT ck_iam_menu_channel CHECK (channel IN ('admin', 'biz')),
    CONSTRAINT ck_iam_menu_node_type CHECK (node_type IN ('catalog', 'menu', 'iframe', 'link')),
    CONSTRAINT ck_iam_menu_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT ck_iam_menu_flags CHECK (hide_in_menu IN (0, 1) AND keep_alive IN (0, 1)),
    CONSTRAINT ck_iam_menu_parent_self CHECK (parent_code IS NULL OR parent_code <> code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='前端菜单 / 路由资源；可见性由权限码推导，不存放授权关系';

-- 000037 (up): 配置中心首次安装态（ADR-0017）。
--
-- status: pending | installing | ready | failed
-- version 为 CAS 乐观锁；owner + lease_until 做多副本互斥（无选主）。

CREATE TABLE IF NOT EXISTS config_install_state (
    env           VARCHAR(16)  NOT NULL COMMENT '环境：与 CONFIGCENTER_ENV 对齐',
    status        VARCHAR(16)  NOT NULL COMMENT 'pending/installing/ready/failed',
    owner         VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '持锁安装者标识；空表示无锁',
    lease_until   DATETIME(3)  NULL COMMENT '租约截止（UTC）；NULL=无租约',
    version       BIGINT       NOT NULL DEFAULT 1 COMMENT '乐观锁版本，begin/complete CAS +1',
    last_error    VARCHAR(512) NOT NULL DEFAULT '' COMMENT '最近失败摘要；成功时清空',
    updated_at    DATETIME(3)  NOT NULL COMMENT '更新时间（UTC）',
    completed_at  DATETIME(3)  NULL COMMENT '进入 ready 的时间（UTC）；未完成则为 NULL',
    PRIMARY KEY (env)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
    COMMENT='配置中心首次安装态（ADR-0017）；真相源，pointer 仅作 complete 校验';

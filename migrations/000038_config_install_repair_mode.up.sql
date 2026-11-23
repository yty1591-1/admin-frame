-- 000038 (up)：安装态增加 mode / repair_keys（ADR-0018 repair 态）。
--
-- mode：first_install | repair；repair 轮次冻结阻塞键白名单。
-- repair_keys：JSON 数组，元素形如 "platform/http.base_url"；非 repair 态为 NULL。
-- expand-only：两列均可空或有默认值，旧版本进程可继续读写。

ALTER TABLE config_install_state
    ADD COLUMN mode        VARCHAR(16) NOT NULL DEFAULT 'first_install' COMMENT 'first_install/repair（ADR-0018）' AFTER status,
    ADD COLUMN repair_keys JSON        NULL COMMENT 'repair 冻结的阻塞键白名单（namespace/key）' AFTER owner;

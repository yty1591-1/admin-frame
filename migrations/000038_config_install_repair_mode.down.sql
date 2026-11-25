-- 000038 (down)：回滚安装态 mode / repair_keys。

ALTER TABLE config_install_state
    DROP COLUMN repair_keys,
    DROP COLUMN mode;

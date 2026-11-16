-- 000036 (up): 移除 config_release 快照签名列（ADR-0016）。
--
-- 拉取模型信任 TLS + 服务身份；签名列不再写入，删除以免误用。

ALTER TABLE config_release
    DROP COLUMN snapshot_signature,
    DROP COLUMN snapshot_key_id;

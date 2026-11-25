-- 000036 (down): 恢复 config_release 快照签名列（ADR-0016 回滚）。

ALTER TABLE config_release
    ADD COLUMN snapshot_signature VARCHAR(256) NOT NULL DEFAULT ''
        COMMENT 'Ed25519 快照签名（base64）；ADR-0016 后已废弃' AFTER snapshot_size,
    ADD COLUMN snapshot_key_id VARCHAR(64) NOT NULL DEFAULT ''
        COMMENT '签名密钥版本标识；ADR-0016 后已废弃' AFTER snapshot_signature;

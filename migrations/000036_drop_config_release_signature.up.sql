-- 000036 (up): 移除 config_release 快照签名列（ADR-0016）。
--
-- CONTRACT: 代码路径已停写/停读 snapshot_signature、snapshot_key_id
-- （UpdateSnapshotSignature 为 no-op，releaseColumns 已剔除）；拉取模型信任
-- TLS + 服务身份，删除列以免误用。

ALTER TABLE config_release
    DROP COLUMN snapshot_signature,
    DROP COLUMN snapshot_key_id;

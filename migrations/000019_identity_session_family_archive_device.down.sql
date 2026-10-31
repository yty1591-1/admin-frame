-- 回滚 000019：删除归档表的设备画像列（MNT-06）。
--
-- 注意：回滚会永久丢失已归档行中的设备画像；在线表与归档主键、清理语义不受影响。

ALTER TABLE identity_session_family_archive
    DROP COLUMN ip_masked,
    DROP COLUMN browser_name,
    DROP COLUMN os_name,
    DROP COLUMN device_type;

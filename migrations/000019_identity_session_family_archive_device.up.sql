-- 设备会话归档表补齐设备画像列（MNT-06）。
--
-- 背景：
--   000013 给在线表增加了 device_type / os_name / browser_name / ip_masked，
--   但 000010 创建的归档表没有这些列；清理任务归档时只能丢弃画像，
--   历史设备无法还原，用户与审计无法判断「哪台设备何时登录」。
-- 变更：
--   1. 归档表补齐与在线表一致的四个画像列（长度与在线表相同）；
--   2. 归档写入改为 INSERT ... SELECT 同步复制这四列；
--   3. 历史归档行保持空值，前端回退「未知设备」，不做回填（无原始数据）。
-- 回滚：
--   执行 000019_identity_session_family_archive_device.down.sql 删除四列；
--   删除后归档写入仍可运行（新列仅影响复制，不影响主键与清理语义）。

ALTER TABLE identity_session_family_archive
    ADD COLUMN device_type   VARCHAR(16) NOT NULL DEFAULT ''
        COMMENT '设备形态：desktop / mobile / tablet；无法识别为空' AFTER ua_hash,
    ADD COLUMN os_name       VARCHAR(32) NOT NULL DEFAULT ''
        COMMENT '操作系统：windows / macos / ios / android / linux / chromeos' AFTER device_type,
    ADD COLUMN browser_name  VARCHAR(32) NOT NULL DEFAULT ''
        COMMENT '浏览器：chrome / edge / safari / firefox / wechat / opera' AFTER os_name,
    ADD COLUMN ip_masked     VARCHAR(64) NOT NULL DEFAULT ''
        COMMENT '脱敏客户端 IP：IPv4 保留前两段，IPv6 保留前四组' AFTER browser_name;

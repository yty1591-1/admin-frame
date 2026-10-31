-- 设备会话补充可展示的设备画像。
--
-- 背景：
--   会话列表此前只返回 ip_hash / ua_hash（带密钥摘要），前端无法还原设备，
--   只能统一展示「未知设备」，用户无法分辨并管理自己的登录设备。
-- 变更：
--   登录 / 刷新时由服务端解析 User-Agent 并脱敏 IP，只落库粗粒度画像；
--   IP / UA 原文仍然禁止落库。画像仅用于展示，不参与鉴权或风控判定。
-- 兼容：
--   历史会话保持空值，前端回退「未知设备」；下次刷新时若画像为空会自动补全。

ALTER TABLE identity_session_family
    ADD COLUMN device_type   VARCHAR(16) NOT NULL DEFAULT ''
        COMMENT '设备形态：desktop / mobile / tablet；无法识别为空' AFTER ua_hash,
    ADD COLUMN os_name       VARCHAR(32) NOT NULL DEFAULT ''
        COMMENT '操作系统：windows / macos / ios / android / linux / chromeos' AFTER device_type,
    ADD COLUMN browser_name  VARCHAR(32) NOT NULL DEFAULT ''
        COMMENT '浏览器：chrome / edge / safari / firefox / wechat / opera' AFTER os_name,
    ADD COLUMN ip_masked     VARCHAR(64) NOT NULL DEFAULT ''
        COMMENT '脱敏客户端 IP：IPv4 保留前两段，IPv6 保留前四组' AFTER browser_name;

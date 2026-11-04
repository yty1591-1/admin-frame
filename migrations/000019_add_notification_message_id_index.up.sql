-- 000019 (up): 回执对账索引（ENT-10 NT-07）——渠道消息 ID 查询。
--
-- 回执回调按 (channel, message_id) 定位投递明细；无索引会导致全表扫描，
-- 且回执是供应商重放的高频路径。

CREATE INDEX idx_delivery_message ON notification_delivery (channel, message_id);

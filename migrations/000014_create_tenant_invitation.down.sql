-- 000014 (down): 回滚邀请表（T-14）。
--
-- 邀请是短期凭证，删除即全部失效；down 仅用于开发环境结构还原，
-- 生产回滚前应先确认没有未处理的 pending 邀请。

DROP TABLE IF EXISTS tenant_invitation;

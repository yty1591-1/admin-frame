-- 账号展示昵称：个人中心唯一可自助编辑的字段。
--
-- 设计要点：
--   1. 与 subject 分离：subject 是登录主体（规范化 + 唯一），昵称只是展示名，
--      允许重复、允许为空；为空时前端回退 subject，避免出现空白卡片；
--   2. 不参与任何唯一索引与鉴权判断，改名不影响会话与令牌；
--   3. 默认空串而非 NULL：避免 Go 侧 `sql.NullString` 与三值逻辑扩散；
--   4. 不写 display_name 的历史数据等价于「未设置昵称」，无需回填。
--
-- 回滚：DROP COLUMN 会丢弃昵称数据，回滚前需确认已接受该损失。

ALTER TABLE identity_account
    ADD COLUMN display_name VARCHAR(64) NOT NULL DEFAULT ''
        COMMENT '展示昵称；空串=未设置，前端回退 subject' AFTER subject;

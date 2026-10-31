-- 商户 ID 规范化（BIZ-06）。
--
-- 背景：
--   identity_account.merchant_id 只做 trim，未统一大小写；库排序规则
--   utf8mb4_0900_ai_ci 大小写不敏感，`M-1` 与 `m-1` 会被唯一键判为重复，
--   但应用侧按不同租户处理，导致 B 端登录 / 绑定结果不一致。
-- 变更：
--   1. 领域层新增 NormalizeMerchantID（trim + lower），所有新建与登录查询统一使用；
--   2. 本迁移把存量 merchant_id 统一为小写，与唯一键口径对齐。
-- 注意：
--   若存量同时存在 `M-1` 与 `m-1` 且关联同一登录主体，UPDATE 会因唯一键冲突失败；
--   执行前用以下语句预检并人工合并：
--     SELECT LOWER(merchant_id), COUNT(DISTINCT merchant_id)
--       FROM identity_account WHERE merchant_id <> ''
--      GROUP BY LOWER(merchant_id) HAVING COUNT(DISTINCT merchant_id) > 1;
-- 回滚：
--   规范化不可逆（原始大小写无法恢复）；见 down 脚本说明。

UPDATE identity_account
   SET merchant_id = LOWER(merchant_id)
 WHERE merchant_id <> LOWER(merchant_id);

-- 回滚：删除访问复核表（先删子表，避免外键约束）。
DROP TABLE IF EXISTS iam_certification_task;
DROP TABLE IF EXISTS iam_certification_campaign;

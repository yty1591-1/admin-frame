-- 000031 (down): 回滚配置中心 M1 数据模型（不可逆删除全部配置、发布与审批数据）。
-- 依赖顺序：审批 -> 指针 -> 快照条目 -> 发布 -> 版本 -> 键。
-- 执行前必须确认已有备份；生产环境回滚须走变更审批并留痕。

DROP TABLE IF EXISTS config_release_review;
DROP TABLE IF EXISTS config_release_pointer;
DROP TABLE IF EXISTS config_release_item;
DROP TABLE IF EXISTS config_release;
DROP TABLE IF EXISTS config_item_version;
DROP TABLE IF EXISTS config_item;

-- 000017 (down): 回滚通知模板种子。

DELETE FROM notification_template WHERE id = 'seed-invitation-email-zh';

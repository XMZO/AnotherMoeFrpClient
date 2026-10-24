-- 0006：去掉 VIP 角色。原来的 VIP 用户改成普通用户；节点"开放给哪些角色"和角色限额里去掉 vip。
-- users.role 的 CHECK 约束和 nodes.roles 的默认值里还留着 vip（SQLite 改不了），代码里不再认它。

-- 和改角色一样加 state_version，客户端下次同步时拿到新角色
UPDATE users SET role = 'user', state_version = state_version + 1, updated_at = CAST(strftime('%s', 'now') AS INTEGER) WHERE role = 'vip';

UPDATE nodes
   SET roles = (SELECT json_group_array(value) FROM json_each(nodes.roles) WHERE value != 'vip')
 WHERE json_valid(roles);

UPDATE settings SET value = json_remove(value, '$.vip') WHERE key = 'role_quotas' AND json_valid(value);

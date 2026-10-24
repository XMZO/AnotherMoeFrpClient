-- 0005：官方节点（docs/node-agent.md 第 12 节）。
-- 节点可以"下发给用户"，按角色开放，个别用户可以单独允许或禁止；限额按角色设，个别用户可以覆盖。

ALTER TABLE nodes ADD COLUMN official INTEGER NOT NULL DEFAULT 0;              -- 是否下发给用户
ALTER TABLE nodes ADD COLUMN roles TEXT NOT NULL DEFAULT '["user","vip","admin"]'; -- 哪些角色可以用（JSON）
ALTER TABLE nodes ADD COLUMN description TEXT NOT NULL DEFAULT '';           -- 给用户看的说明

ALTER TABLE users ADD COLUMN node_cred_gen INTEGER NOT NULL DEFAULT 0;      -- 节点凭证的代数，重置凭证时 +1
ALTER TABLE users ADD COLUMN node_quota TEXT NOT NULL DEFAULT '';          -- 对角色限额的覆盖（JSON），空表示不覆盖

-- 单个用户对单个节点的授权：allow = 1 允许（不管角色），0 禁止
CREATE TABLE node_grants (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  allow   INTEGER NOT NULL,
  PRIMARY KEY (user_id, node_id)
);

-- 面板级的设置：角色限额、节点凭证的密钥等
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

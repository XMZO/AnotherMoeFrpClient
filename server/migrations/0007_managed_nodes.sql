-- 0007：节点全部由面板托管（docs/node-agent.md 第 4、12 节）。
-- frps 不再有 token，只认面板签发的登录票据；frps 配置从 TOML 改成结构化的 frpsset.Settings；
-- 节点和客户端设备都记下指纹，凭据被拷到别的机器上就用不了。

ALTER TABLE nodes ADD COLUMN settings TEXT NOT NULL DEFAULT '';     -- 结构化 frps 配置（JSON），取代 frps_config（旧 TOML 在启动时自动转换）
ALTER TABLE nodes ADD COLUMN machine_id TEXT NOT NULL DEFAULT '';   -- agent 上报的机器指纹，第一次上线时记下，重置令牌时清空
ALTER TABLE sessions ADD COLUMN device_id TEXT NOT NULL DEFAULT ''; -- 客户端设备指纹，领官方节点票据时要对得上

-- 原来没开"官方节点"的节点：只给管理员用（管理员默认能用所有节点）
UPDATE nodes SET roles = '[]' WHERE official = 0;

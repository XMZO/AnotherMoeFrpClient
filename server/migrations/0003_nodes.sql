-- 0003：frps 节点和节点上的任务（docs/node-agent.md 第 9 节）。

CREATE TABLE nodes (
  id                TEXT PRIMARY KEY,
  name              TEXT NOT NULL,
  public_addr       TEXT NOT NULL DEFAULT '',     -- 客户端连接用的地址，空时用 agent 上报的 IP
  token_hash        BLOB UNIQUE,                  -- sha256(长期令牌)，注册前为空
  enroll_hash       BLOB UNIQUE,                  -- sha256(一次性安装码)，用过或过期后清空
  enroll_expires_at INTEGER,
  frps_config       TEXT NOT NULL DEFAULT '',     -- 期望的 frps TOML
  config_rev        INTEGER NOT NULL DEFAULT 0,   -- 期望配置的版本号，每次修改 +1
  applied_rev       INTEGER NOT NULL DEFAULT 0,   -- agent 上报的已生效版本
  apply_error       TEXT NOT NULL DEFAULT '',     -- 最近一次下发失败的原因
  pending_uninstall INTEGER NOT NULL DEFAULT 0,   -- 离线时点了卸载，上线后执行
  info              TEXT NOT NULL DEFAULT '{}',   -- 最近一次 hello（JSON）
  last_ip           TEXT NOT NULL DEFAULT '',
  last_seen_at      INTEGER,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
);

CREATE TABLE node_tasks (
  id          TEXT PRIMARY KEY,
  node_id     TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL CHECK (kind IN ('exec','upgrade')),
  command     TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL CHECK (status IN ('running','done','failed')),
  exit_code   INTEGER,
  output      TEXT NOT NULL DEFAULT '',
  error       TEXT NOT NULL DEFAULT '',
  created_by  TEXT,                               -- 用户 ID
  created_at  INTEGER NOT NULL,
  finished_at INTEGER
);
CREATE INDEX idx_node_tasks_node ON node_tasks(node_id, created_at);

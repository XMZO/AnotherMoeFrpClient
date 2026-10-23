-- 0001：初始表结构（PLAN 7.3）。所有时间字段都是 Unix 秒。

CREATE TABLE users (
  id             TEXT PRIMARY KEY,               -- 沿用旧库的 uuid
  nickname       TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash  TEXT NOT NULL,                  -- argon2id PHC 字符串
  role           TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user','vip','admin')),
  status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  state_version  INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);

CREATE TABLE sessions (
  id            TEXT PRIMARY KEY,
  user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash    BLOB NOT NULL UNIQUE,            -- sha256(token)
  install_id    TEXT NOT NULL,
  device_name   TEXT,
  created_at    INTEGER NOT NULL,
  last_seen_at  INTEGER NOT NULL,
  expires_at    INTEGER NOT NULL,
  revoked_at    INTEGER,
  revoke_reason TEXT                             -- logout / replaced / password_changed / admin
);
CREATE INDEX idx_sessions_user ON sessions(user_id);

CREATE TABLE invite_codes (
  code        TEXT PRIMARY KEY,                  -- FRPT-XXXX-XXXX
  note        TEXT,
  created_at  INTEGER NOT NULL,
  used_by     TEXT REFERENCES users(id) ON DELETE SET NULL,
  used_at     INTEGER,
  revoked_at  INTEGER
);

CREATE TABLE password_reset_codes (
  code_hash   BLOB PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at  INTEGER NOT NULL,
  expires_at  INTEGER NOT NULL,
  used_at     INTEGER
);

CREATE TABLE profiles (
  id              TEXT PRIMARY KEY,
  owner_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  data            TEXT NOT NULL,                 -- JSON: {nodes, proxies, visitors}
  schema_version  INTEGER NOT NULL DEFAULT 1,
  revision        INTEGER NOT NULL DEFAULT 1,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
CREATE INDEX idx_profiles_owner ON profiles(owner_id);

CREATE TABLE shares (
  id                 TEXT PRIMARY KEY,           -- 128 位随机数，base62 编码
  owner_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name               TEXT NOT NULL,
  kind               TEXT NOT NULL CHECK (kind IN ('template','full','p2p')),
  payload            TEXT NOT NULL,              -- JSON 快照
  source_profile_id  TEXT REFERENCES profiles(id) ON DELETE SET NULL,
  revision           INTEGER NOT NULL DEFAULT 1,
  expires_at         INTEGER,
  max_subscribers    INTEGER,
  revoked_at         INTEGER,
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL
);
CREATE INDEX idx_shares_owner ON shares(owner_id);

CREATE TABLE subscriptions (
  id          TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  share_id    TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
  params      TEXT NOT NULL,                     -- JSON，结构随 kind 不同
  revision    INTEGER NOT NULL DEFAULT 1,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  UNIQUE (user_id, share_id)
);

CREATE TABLE audit_log (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  at        INTEGER NOT NULL,
  actor_id  TEXT,
  action    TEXT NOT NULL,
  target    TEXT,
  ip        TEXT,
  detail    TEXT
);

CREATE TABLE schema_migrations (
  version     INTEGER PRIMARY KEY,
  applied_at  INTEGER NOT NULL
);

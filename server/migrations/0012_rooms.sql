-- 0012：联机房间（虚拟局域网，shared/api/vnet.go、docs/vnet.md）。
-- slot 是座位号（1–250），虚拟 IP 是 10.241.0.<slot>，在房间里唯一（成员和游客共用），离开后空出来。
CREATE TABLE rooms (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    code         TEXT NOT NULL UNIQUE,
    owner_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   INTEGER NOT NULL,
    allow_guests INTEGER NOT NULL DEFAULT 0, -- 允许没登录的人以游客身份进来
    relay        TEXT NOT NULL DEFAULT '',   -- 默认中继节点，空表示自动
    relay_locked INTEGER NOT NULL DEFAULT 0  -- 锁定中继：成员不能自己换
);
CREATE TABLE room_members (
    room_id   TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    slot      INTEGER NOT NULL,
    joined_at INTEGER NOT NULL,
    PRIMARY KEY (room_id, user_id)
);
CREATE INDEX room_members_user ON room_members(user_id);
-- 游客：没有账号，用邀请码 + 昵称进来，拿到只能用于这个房间的令牌（只存哈希）
CREATE TABLE room_guests (
    id         TEXT PRIMARY KEY,
    room_id    TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    nickname   TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    slot       INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX room_guests_room ON room_guests(room_id);

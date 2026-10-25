-- 0012：联机房间（虚拟局域网，shared/api/vnet.go）。
-- slot 是成员的座位号（1–250），虚拟 IP 是 10.241.0.<slot>，在房间里唯一，离开后空出来。
CREATE TABLE rooms (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    code       TEXT NOT NULL UNIQUE,
    owner_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL
);
CREATE TABLE room_members (
    room_id   TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    slot      INTEGER NOT NULL,
    joined_at INTEGER NOT NULL,
    PRIMARY KEY (room_id, user_id),
    UNIQUE (room_id, slot)
);
CREATE INDEX room_members_user ON room_members(user_id);

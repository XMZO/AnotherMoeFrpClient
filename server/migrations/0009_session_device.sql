-- 0009：登录设备的信息（管理后台"用户 → 登录设备"，docs/client-live.md）。
-- login_ip：登录时的 IP；last_ip：最近一次连实时通道的 IP；
-- client_info：客户端连实时通道时上报的设备信息（JSON：版本、系统、内部版本号、架构、CPU、内存、型号、主机名），离线后也能看到。
ALTER TABLE sessions ADD COLUMN login_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN last_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN client_info TEXT NOT NULL DEFAULT '';

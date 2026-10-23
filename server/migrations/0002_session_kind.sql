-- 会话类型：client（桌面客户端）或 admin_web（管理后台网页）。
-- 单设备模式只在同一类型之间互相挤下线，打开管理后台不会把桌面客户端挤掉。
ALTER TABLE sessions ADD COLUMN kind TEXT NOT NULL DEFAULT 'client';

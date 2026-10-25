-- 0013：联机房间的语音（docs/vnet.md）。no_voice：房主关了语音；voice_muted：被房主禁言的座位号（逗号分隔）。
ALTER TABLE rooms ADD COLUMN no_voice INTEGER NOT NULL DEFAULT 0;
ALTER TABLE rooms ADD COLUMN voice_muted TEXT NOT NULL DEFAULT '';

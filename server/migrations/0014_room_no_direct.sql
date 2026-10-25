-- 0014：联机房间的"禁止直连"（docs/vnet.md）：打开后成员之间不打洞，全部走中继（也不会互相看到公网 IP）。
ALTER TABLE rooms ADD COLUMN no_direct INTEGER NOT NULL DEFAULT 0;

-- 0008：节点的用户访问地址（docs/node-agent.md 第 12 节）。
-- 用户在客户端里看到并复制，用来访问自己映射在这个节点上的 tcp / udp 代理。
-- JSON {"domain","ipv4","ipv6"}：空表示自动检测（IP），"-" 表示不显示。
ALTER TABLE nodes ADD COLUMN access TEXT NOT NULL DEFAULT '';

-- 0011：节点的地区分类（中国大陆 cn / 港澳台 hmt / 境外 intl）。客户端的官方节点按地区分组显示。
-- 新建节点必须选；这之前建的节点为空，管理后台提示去设置。
ALTER TABLE nodes ADD COLUMN region TEXT NOT NULL DEFAULT '';

-- 0010：节点排序（管理后台"节点 → 排序"）。管理后台的节点列表和客户端的官方节点列表都按它排，相同时按创建时间。
ALTER TABLE nodes ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;

-- 0004：每个节点自己的下载代理（安装命令和升级都用它，空表示直连 GitHub）。
ALTER TABLE nodes ADD COLUMN download_mirror TEXT NOT NULL DEFAULT '';

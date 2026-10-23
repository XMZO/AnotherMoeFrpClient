#!/bin/sh
# MoeFrp 节点 agent 安装脚本（由面板生成，面板地址已写好）。
#
#   安装：curl -fsSL __SERVER__/agent/install.sh | sudo sh -s -- <安装码> [--no-exec]
#   卸载：curl -fsSL __SERVER__/agent/install.sh | sudo sh -s -- --uninstall
#
# 只支持 Linux + systemd，amd64 / arm64。程序装在 /usr/local/bin/moefrp-agent，
# 配置在 /etc/moefrp-agent，状态在 /var/lib/moefrp-agent，服务名 moefrp-agent。
set -eu

SERVER='__SERVER__'

fail() { echo "错误：$*" >&2; exit 1; }

ACTION=install
CODE=
EXTRA=
for arg in "$@"; do
	case "$arg" in
	--uninstall) ACTION=uninstall ;;
	--no-exec | --force) EXTRA="$EXTRA $arg" ;;
	-*) fail "不认识的参数 $arg" ;;
	*) CODE=$arg ;;
	esac
done

[ "$(id -u)" = 0 ] || fail "需要 root 权限，请在命令里加 sudo"

if [ "$ACTION" = uninstall ]; then
	if [ -x /usr/local/bin/moefrp-agent ]; then
		exec /usr/local/bin/moefrp-agent uninstall
	fi
	# 程序已经不在了：按清单清理
	systemctl disable --now moefrp-agent.service 2>/dev/null || true
	rm -rf /etc/systemd/system/moefrp-agent.service /etc/moefrp-agent /var/lib/moefrp-agent /usr/local/bin/moefrp-agent
	systemctl daemon-reload 2>/dev/null || true
	systemctl reset-failed moefrp-agent.service 2>/dev/null || true
	echo "卸载完成"
	exit 0
fi

[ "$(uname -s)" = Linux ] || fail "只支持 Linux"
[ -d /run/systemd/system ] || fail "这台机器没有用 systemd，暂不支持"
case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) fail "不支持的架构 $(uname -m)，只支持 amd64 和 arm64" ;;
esac
if [ -z "$CODE" ] && [ ! -f /etc/moefrp-agent/agent.toml ]; then
	fail "缺少安装码。请在面板的“节点”页复制完整的安装命令"
fi

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
else
	fail "需要 curl 或 wget"
fi

# 下载到 /var/lib（/tmp 可能挂成 noexec），装好后删掉
mkdir -p /var/lib/moefrp-agent
chmod 700 /var/lib/moefrp-agent
TMP=$(mktemp -d /var/lib/moefrp-agent/install.XXXXXX)
trap 'rm -rf "$TMP"' EXIT INT TERM

echo "下载 moefrp-agent（linux-$ARCH）……"
fetch "$SERVER/agent/download/linux-$ARCH" "$TMP/moefrp-agent" || fail "下载失败"
fetch "$SERVER/agent/download/linux-$ARCH.sha256" "$TMP/sha256" || fail "下载校验和失败"
WANT=$(cut -d ' ' -f 1 "$TMP/sha256")
if command -v sha256sum >/dev/null 2>&1; then
	GOT=$(sha256sum "$TMP/moefrp-agent" | cut -d ' ' -f 1)
else
	GOT=$(openssl dgst -sha256 -r "$TMP/moefrp-agent" | cut -d ' ' -f 1)
fi
[ "$WANT" = "$GOT" ] || fail "校验失败：期望 $WANT，实际 $GOT"
chmod 755 "$TMP/moefrp-agent"

# shellcheck disable=SC2086
"$TMP/moefrp-agent" install --server "$SERVER" --code "$CODE" $EXTRA

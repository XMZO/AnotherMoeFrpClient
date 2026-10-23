#!/usr/bin/env bash
# 节点 agent 的真机冒烟测试（GitHub Actions 的 ubuntu 机器有 systemd 和免密 sudo）：
# 用镜像里的面板 → 面板给的安装命令安装 → 下发 frps 配置 → 执行命令 → 从旧版本升级 → 面板卸载，
# 最后检查机器上没有残留。另外测一遍 install.sh --uninstall。
#
#   scripts/ci/agent-smoke.sh <镜像名> <旧版本 agent 程序>
set -euxo pipefail

IMAGE=$1
OLD_AGENT=$2
BASE=http://127.0.0.1:18090
JAR=$(mktemp)
DATA=$(mktemp -d)

# set -e 不管 "! 命令" 的结果，否定的断言要用这个
must_fail() { if "$@"; then echo "不应成功：$*" >&2; exit 1; fi; }
api() { curl -fsS -b "$JAR" -c "$JAR" -H 'X-MoeFrp-Admin: 1' -H 'Content-Type: application/json' "$@"; }
wait_for() { # 描述 次数 命令...
  local what=$1 n=$2; shift 2
  for _ in $(seq "$n"); do "$@" && return 0; sleep 1; done
  echo "等待超时：$what" >&2
  return 1
}
node_field() { api "$BASE/api/v1/admin/nodes/$1" | jq -r "$2"; }
leftovers() {
  local left=0
  for p in /usr/local/bin/moefrp-agent /etc/moefrp-agent /var/lib/moefrp-agent /etc/systemd/system/moefrp-agent.service; do
    if sudo test -e "$p"; then echo "残留：$p"; left=1; fi
  done
  if systemctl list-unit-files 'moefrp-agent*' --no-legend | grep -q .; then echo "残留 systemd 单元"; left=1; fi
  return $left
}
cleanup() {
  sudo journalctl -u moefrp-agent --no-pager -n 80 || true
  docker logs smoke-panel 2>&1 | tail -n 40 || true
  docker rm -f smoke-panel >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run -d --name smoke-panel -p 127.0.0.1:18090:8080 -v "$DATA:/data" \
  -e MOEFRP_ADMIN_SETUP_CODE=CI-ADMIN-SETUP-CODE "$IMAGE"
wait_for "面板启动" 30 curl -fsS "$BASE/healthz"
api -X POST "$BASE/api/v1/admin/setup" -d '{"code":"CI-ADMIN-SETUP-CODE","nickname":"ci_admin","password":"ci-admin-password"}' >/dev/null
api -X POST "$BASE/api/v1/admin/elevate" -d '{"password":"ci-admin-password"}' >/dev/null
VERSION=$(api "$BASE/api/v1/admin/nodes" | jq -r .agent.version)
test -n "$VERSION" && test "$VERSION" != null

# ---- 1. 用面板给的安装命令安装 ----
NODE=$(api -X POST "$BASE/api/v1/admin/nodes" -d '{"name":"ci-node"}')
ID=$(jq -r .node.id <<<"$NODE")
CMD=$(jq -r .installCommand <<<"$NODE")
echo "$CMD"
bash -c "$CMD"
systemctl is-active --quiet moefrp-agent
sudo stat -c '%a %U' /etc/moefrp-agent/agent.toml | grep -qx '600 root'
must_fail grep -q mfn_ /etc/systemd/system/moefrp-agent.service # 令牌不在单元文件里
wait_for "节点上线" 30 bash -c "[ \"\$(curl -fsS -b $JAR -H 'X-MoeFrp-Admin: 1' $BASE/api/v1/admin/nodes/$ID | jq -r .status)\" = online ]"
test "$(node_field "$ID" .info.agentVersion)" = "$VERSION"

# ---- 2. frps 配置：生效、端口被占时回滚 ----
R=$(api -X PUT "$BASE/api/v1/admin/nodes/$ID/config" -d '{"config":"bindPort = 17000\nauth.token = \"ci-token\"\n"}')
jq -e '.pushed and .result.applied' <<<"$R"
wait_for "frps 监听 17000" 10 bash -c 'echo > /dev/tcp/127.0.0.1/17000'
R=$(api -X PUT "$BASE/api/v1/admin/nodes/$ID/config" -d '{"config":"bindPort = 18090\n"}') # 被面板占用
jq -e '.result.applied == false and .result.rolledBack == true' <<<"$R"
bash -c 'echo > /dev/tcp/127.0.0.1/17000'

# ---- 3. 以 root 执行命令 ----
TID=$(api -X POST "$BASE/api/v1/admin/nodes/exec" -d "{\"nodes\":[\"$ID\"],\"command\":\"id -u; head -1 /etc/os-release\"}" | jq -r '.tasks[0].id')
wait_for "命令完成" 20 bash -c "[ \"\$(curl -fsS -b $JAR -H 'X-MoeFrp-Admin: 1' $BASE/api/v1/admin/tasks/$TID | jq -r .status)\" = done ]"
api "$BASE/api/v1/admin/tasks/$TID" | jq -r .output | head -1 | grep -qx 0

# ---- 4. 从旧版本升级到面板版本 ----
sudo systemctl stop moefrp-agent
sudo install -m 755 "$OLD_AGENT" /usr/local/bin/moefrp-agent
sudo systemctl start moefrp-agent
wait_for "旧版本上线" 30 bash -c "[ \"\$(curl -fsS -b $JAR -H 'X-MoeFrp-Admin: 1' $BASE/api/v1/admin/nodes/$ID | jq -r .info.agentVersion)\" = ci-old ]"
test "$(node_field "$ID" .upgradeTo)" = "$VERSION"
UID_TASK=$(api -X POST "$BASE/api/v1/admin/nodes/$ID/upgrade" | jq -r .id)
wait_for "升级完成" 60 bash -c "[ \"\$(curl -fsS -b $JAR -H 'X-MoeFrp-Admin: 1' $BASE/api/v1/admin/tasks/$UID_TASK | jq -r .status)\" = done ]"
test "$(node_field "$ID" .info.agentVersion)" = "$VERSION"
test "$(/usr/local/bin/moefrp-agent version)" = "$VERSION"
wait_for "升级临时文件清理" 10 bash -c '! sudo test -e /var/lib/moefrp-agent/moefrp-agent.bak && ! sudo test -e /var/lib/moefrp-agent/upgrade.json'
bash -c 'echo > /dev/tcp/127.0.0.1/17000' # frps 用原来的配置继续运行

# ---- 5. 面板卸载，检查没有残留 ----
api -X POST "$BASE/api/v1/admin/nodes/$ID/uninstall" | jq -e .done
wait_for "卸载完成" 30 leftovers
must_fail systemctl is-active --quiet moefrp-agent
must_fail bash -c 'echo > /dev/tcp/127.0.0.1/17000 2>/dev/null' # frps 已停止
test "$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" -H 'X-MoeFrp-Admin: 1' "$BASE/api/v1/admin/nodes/$ID")" = 404
wait_for "临时卸载单元结束" 20 bash -c '! systemctl list-units --all --no-legend "moefrp-agent*" | grep -q .'

# ---- 6. 再装一次，用 install.sh --uninstall 卸载 ----
CMD=$(api -X POST "$BASE/api/v1/admin/nodes" -d '{"name":"ci-node-2"}' | jq -r .installCommand)
bash -c "$CMD --no-exec"
systemctl is-active --quiet moefrp-agent
sudo grep -qx 'exec = false' /etc/moefrp-agent/agent.toml
curl -fsSL "$BASE/agent/install.sh" | sudo sh -s -- --uninstall
leftovers

echo "agent smoke test passed"

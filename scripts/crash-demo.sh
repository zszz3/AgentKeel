#!/usr/bin/env bash
# 崩溃恢复演示：用假模型修 testdata/fixtest，在第一个工具调用跑到一半时 kill -9，再 resume。
# 写完 Replay 和 Run 之后跑：./scripts/crash-demo.sh
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
port=${PORT:-18777}
cleanup() { [[ -n "${model_pid:-}" ]] && kill "$model_pid" 2>/dev/null || true; }
trap cleanup EXIT

cd "$root"
go build -o "$tmp/agentkeel" ./cmd/agentkeel
go build -o "$tmp/fakemodel" ./cmd/fakemodel
cp -R testdata/fixtest "$tmp/work"

"$tmp/fakemodel" -addr "127.0.0.1:$port" 2>"$tmp/model.log" &
model_pid=$!
sleep 0.5

export AGENTKEEL_BASE_URL="http://127.0.0.1:$port" AGENTKEEL_API_KEY=fake AGENTKEEL_MODEL=fake
cd "$tmp"

echo "== run，2 秒后 kill -9（第一个命令要跑 5 秒）"
./agentkeel -db ak.db run -workdir work "让 go test 通过" &
agent_pid=$!
sleep 2
kill -9 "$agent_pid"
wait "$agent_pid" 2>/dev/null || true

id=$(./agentkeel -db ak.db sessions | head -1)
echo
echo "== resume $id"
./agentkeel -db ak.db resume "$id"

echo
echo "== 完整事件日志"
./agentkeel -db ak.db log "$id"

echo
echo "== 模型被调用的情况"
cat model.log

echo
echo "== 最后在 work 目录跑测试"
(cd work && go test ./...)
echo
echo "临时目录：$tmp"

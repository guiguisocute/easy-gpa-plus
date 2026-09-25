#!/usr/bin/env sh
# HTTP 健康检查；完整业务冒烟使用 node e2e/run.mjs smoke。
set -e
BASE="${BASE:-http://localhost:8080}"

echo "1) healthz"
curl -fsS "$BASE/healthz" | grep -q '"ok"'

echo "2) api ping"
curl -fsS "$BASE/api/v1/ping" | grep -q pong

echo "smoke OK"

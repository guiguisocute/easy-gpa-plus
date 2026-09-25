#!/bin/sh
set -u

binary="${1:?compiled zongce binary is required}"
worker_names="${EASYGPA_DEV_WORKERS:-dispatch notify export maintenance ai agent}"
pids=""

stop_workers() {
  trap - INT TERM HUP
  for pid in $pids; do
    kill -TERM "$pid" 2>/dev/null || true
  done

  # Development reloads must not wait on a long Redis/database poll forever.
  # Give every worker 1.5 seconds to drain, then terminate only the remaining
  # local processes. Production uses its own Compose and grace periods.
  attempts=0
  while [ "$attempts" -lt 15 ]; do
    any_alive=false
    for pid in $pids; do
      if kill -0 "$pid" 2>/dev/null; then
        any_alive=true
        break
      fi
    done
    if [ "$any_alive" = false ]; then
      break
    fi
    attempts=$((attempts + 1))
    sleep 0.1
  done

  for pid in $pids; do
    if kill -0 "$pid" 2>/dev/null; then
      kill -KILL "$pid" 2>/dev/null || true
    fi
  done
  for pid in $pids; do
    wait "$pid" 2>/dev/null || true
  done
}

trap 'stop_workers; exit 0' INT TERM HUP

for worker_name in $worker_names; do
  "$binary" "worker:$worker_name" &
  pids="$pids $!"
done

while :; do
  for pid in $pids; do
    if ! kill -0 "$pid" 2>/dev/null; then
      status=0
      wait "$pid" || status=$?
      if [ "$status" -eq 0 ]; then
        status=1
      fi
      echo "development worker process $pid exited; restarting the worker group" >&2
      stop_workers
      exit "$status"
    fi
  done
  sleep 1
done

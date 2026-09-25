#!/bin/sh
set -eu

binary="${1:?compiled zongce binary is required}"
version_file="${2:?binary version file is required}"
poll_seconds="${EASYGPA_DEV_WORKER_POLL_SECONDS:-0.3}"
group_pid=""
current_version=""

stop_group() {
  if [ -n "$group_pid" ] && kill -0 "$group_pid" 2>/dev/null; then
    kill -TERM "$group_pid" 2>/dev/null || true
    wait "$group_pid" 2>/dev/null || true
  fi
  group_pid=""
}

start_group() {
  while [ ! -x "$binary" ] || [ ! -s "$version_file" ]; do
    sleep "$poll_seconds"
  done

  current_version="$(cat "$version_file")"
  sh /workspace/deploy/development/run-workers.sh "$binary" &
  group_pid="$!"
}

trap 'stop_group; exit 0' INT TERM HUP

start_group
while :; do
  if ! kill -0 "$group_pid" 2>/dev/null; then
    status=0
    wait "$group_pid" || status=$?
    if [ "$status" -eq 0 ]; then
      status=1
    fi
    echo "development worker group exited unexpectedly" >&2
    exit "$status"
  fi

  next_version="$(cat "$version_file" 2>/dev/null || true)"
  if [ -n "$next_version" ] && [ "$next_version" != "$current_version" ]; then
    echo "new development backend binary detected; restarting workers"
    stop_group
    start_group
  fi

  sleep "$poll_seconds"
done

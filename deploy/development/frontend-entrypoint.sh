#!/bin/sh
set -eu

cd /workspace/frontend

lock_hash="$(sha256sum package-lock.json | awk '{print $1}')"
marker="node_modules/.easygpa-package-lock.sha256"
installed_hash=""

if [ -f "$marker" ]; then
  installed_hash="$(cat "$marker")"
fi

if [ ! -d node_modules ] || [ "$installed_hash" != "$lock_hash" ]; then
  echo "package-lock.json changed or dependencies are missing; running npm ci"
  npm ci --no-audit --no-fund
  printf '%s\n' "$lock_hash" > "$marker"
else
  echo "frontend dependencies are already cached"
fi

exec "$@"

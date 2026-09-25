#!/bin/sh
set -eu

binary="/dev-bin/zongce"
if [ "${1:-}" = "--debug" ]; then
  # A debug build must never replace Air's binary or notify running workers.
  trap 'rm -f /dev-bin/zongce.debug.next' EXIT INT TERM HUP
  go build -buildvcs=false -gcflags='all=-N -l' -o /dev-bin/zongce.debug.next ./cmd/zongce
  mv -f /dev-bin/zongce.debug.next /dev-bin/zongce.debug
  exit 0
fi
next_binary="${binary}.next"
version_file="${binary}.version"
next_version="${version_file}.next"

cleanup() {
  rm -f "$next_binary" "$next_version"
}
trap cleanup EXIT INT TERM HUP

# Production embeds a Git SHA in its immutable image. The local watcher does
# not need VCS metadata, and avoiding that scan is materially faster on a
# Windows bind mount.
go build -buildvcs=false -o "$next_binary" ./cmd/zongce
chmod 0755 "$next_binary"
mv -f "$next_binary" "$binary"
printf '%s-%s\n' "$(date +%s)" "$$" > "$next_version"
mv -f "$next_version" "$version_file"

trap - EXIT INT TERM HUP

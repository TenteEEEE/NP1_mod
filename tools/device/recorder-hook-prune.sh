#!/bin/sh
# Retain the four newest complete recorder-hook recovery snapshots.
# Targets are resolved and constrained to NCC's fixed recovery directory.
set -eu
BASE=/overlay/owner-apps/np1-control
RECOVERY=$BASE/recovery
test "$(id -u)" = 0
test -d "$RECOVERY" && test ! -L "$RECOVERY"

count=0
removed=0
for path in $(ls -1dt "$RECOVERY"/recorder-hook-* 2>/dev/null || true); do
    test -d "$path" && test ! -L "$path"
    resolved=$(readlink -f "$path")
    case "$resolved" in "$RECOVERY"/recorder-hook-*) ;; *) echo 'Recorder recovery target escaped fixed directory' >&2; exit 1;; esac
    count=$((count + 1))
    if test "$count" -gt 4; then
        rm -rf -- "$resolved"
        removed=$((removed + 1))
    fi
done
echo "Recorder recovery snapshots removed: $removed; newest retained: $((count - removed))"
df -h /overlay

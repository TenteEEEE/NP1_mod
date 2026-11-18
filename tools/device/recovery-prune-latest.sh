#!/bin/sh
# Retain the active full recovery generation and the newest snapshot per
# updater family. Every recursive target is resolved beneath the fixed NCC
# recovery directory before removal.
set -eu
BASE=/overlay/owner-apps/np1-control
RECOVERY=$BASE/recovery
MODE=${1:-dry-run}
case "$MODE" in dry-run|apply) ;; *) exit 2 ;; esac
test "$(id -u)" = 0
test -d "$RECOVERY" && test ! -L "$RECOVERY"
LATEST=$(cat "$RECOVERY/latest")
LATEST=$(readlink -f "$LATEST")
case "$LATEST" in "$RECOVERY"/[0-9]*T[0-9]*Z) ;; *) echo 'Invalid active recovery generation' >&2; exit 1 ;; esac
test -d "$LATEST" && test ! -L "$LATEST"

removed=0
retained=0
remove_checked() {
    candidate=$1
    resolved=$(readlink -f "$candidate")
    case "$resolved" in "$RECOVERY"/*) ;; *) echo 'Recovery target escaped fixed directory' >&2; exit 1 ;; esac
    test -d "$resolved" && test ! -L "$resolved"
    if test "$resolved" = "$LATEST"; then retained=$((retained + 1)); return; fi
    if test "$MODE" = apply; then rm -rf -- "$resolved"; fi
    echo "$MODE: $resolved"
    removed=$((removed + 1))
}

# Full install generations are ordered in their fixed UTC directory names.
for path in "$RECOVERY"/[0-9]*T[0-9]*Z; do
    test -e "$path" || continue
    if test "$(readlink -f "$path")" = "$LATEST"; then retained=$((retained + 1)); else remove_checked "$path"; fi
done

# Keep the newest complete snapshot from each independent updater family.
for prefix in ap-worker journey modem-worker nas-install native-worker plate-fill plate-merge recorder-hook stock-start-install web-worker; do
    count=0
    for path in $(ls -1dt "$RECOVERY"/"$prefix"-* 2>/dev/null || true); do
        test -d "$path" && test ! -L "$path"
        count=$((count + 1))
        if test "$count" -eq 1; then retained=$((retained + 1)); else remove_checked "$path"; fi
    done
done

echo "Recovery snapshots selected for removal: $removed; retained generations: $retained"
df -h /overlay

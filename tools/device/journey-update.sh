#!/bin/sh
# Update NCC Web/broker and repair radio history as one recoverable owner action.
# Stock services, AP/modem/native/plate workers and network settings stay intact.
set -eu
umask 077
BASE=/overlay/owner-apps/np1-control
STAGE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
test "$(id -u)" = 0
cd "$STAGE"
sha256sum -c SHA256SUMS >/dev/null
chmod 0755 bin/ncc
test "$(bin/ncc --mode radio-history-repair --config "$BASE/config/config.json" --radio-repair-state-dir)" = "$BASE/state"
systemctl is-active --quiet np1-control.service
systemctl is-active --quiet np1-control-broker.service
BACKUP="$BASE/recovery/journey-$(date -u +%Y%m%dT%H%M%SZ)-$$"
mkdir -m 0700 "$BACKUP"
cp -p "$BASE/bin/ncc" "$BACKUP/ncc"
capture(){
    systemctl show np1-control-ap.service np1-control-modem.service np1-control-native.service np1-control-plate.service app-@Communication.service app-@DriveRec.service app-@ImageRecog.service app-@Voice.service qmmf-server.service -p Id -p MainPID -p InvocationID -p ActiveState -p SubState
}
capture > "$BACKUP/preserved-before.txt"
READY=0
CHANGED=0
DONE=0
LED_ACTIVE=0
if systemctl is-active --quiet np1-control-led.service; then LED_ACTIVE=1; fi
finish(){
    result=$?
    trap - 0
    if test "$DONE" != 1; then
        if test "$CHANGED" = 1 && test "$READY" = 1; then
            systemctl stop np1-control.service || result=1
            (cd "$BACKUP" && sha256sum -c SHA256SUMS) || exit 1
            cp -p "$BACKUP/ncc" "$BASE/bin/ncc.journey-rollback"
            mv -f "$BASE/bin/ncc.journey-rollback" "$BASE/bin/ncc"
            for file in radio-observations.jsonl radio-observations.jsonl.1 radio-logger.json; do
                if test -f "$BACKUP/$file"; then cp -p "$BACKUP/$file" "$BASE/state/$file"; else rm -f "$BASE/state/$file"; fi
            done
            systemctl restart np1-control-broker.service || result=1
            if test "$LED_ACTIVE" = 1; then systemctl start np1-control-led.service || result=1; fi
        fi
        systemctl start np1-control.service || result=1
        echo "Journey update failed; NCC recovery: $BACKUP" >&2
    fi
    exit "$result"
}
trap finish 0
trap 'exit 1' HUP INT TERM
systemctl stop np1-control.service
for file in radio-observations.jsonl radio-observations.jsonl.1 radio-logger.json; do
    test ! -L "$BASE/state/$file"
    if test -f "$BASE/state/$file"; then cp -p "$BASE/state/$file" "$BACKUP/$file"; fi
done
(cd "$BACKUP" && sha256sum ncc > SHA256SUMS; for file in radio-observations.jsonl radio-observations.jsonl.1 radio-logger.json; do if test -f "$BACKUP/$file"; then (cd "$BACKUP" && sha256sum "$file") >> "$BACKUP/SHA256SUMS"; fi; done)
READY=1
CHANGED=1
sh tools/web-worker-update.sh
test "$(sha256sum bin/ncc | cut -d ' ' -f 1)" = "$(sha256sum "$BASE/bin/ncc" | cut -d ' ' -f 1)"
test "$(sha256sum bin/ncc | cut -d ' ' -f 1)" = "$(sha256sum /run/np1-control/ncc | cut -d ' ' -f 1)"
sh tools/repair-radio-history.sh apply > "$BACKUP/repair.txt"
capture > "$BACKUP/preserved-after.txt"
cmp "$BACKUP/preserved-before.txt" "$BACKUP/preserved-after.txt"
sh tools/verify.sh
DONE=1
echo "Journey backup: $BACKUP"
cat "$BACKUP/repair.txt"
echo 'PASS: journey update, GPS association repair and preserved service identities'

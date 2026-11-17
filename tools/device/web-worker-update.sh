#!/bin/sh
# Update the owner Web/API and broker, with an optional uplink supervisor restart.
# Backup old owner binary; restore it on failure. No native service action.
set -eu
umask 077
BASE=/overlay/owner-apps/np1-control
UPDATE_UPLINK=0
case "${*:-}" in
    '') ;;
    --uplink) UPDATE_UPLINK=1 ;;
    *) exit 64 ;;
esac
STAGE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
test "$(id -u)" = 0
cd "$STAGE"
sha256sum -c SHA256SUMS >/dev/null
/bin/chmod 0755 bin/ncc
bin/ncc --mode self-test
if test "$UPDATE_UPLINK" = 1; then
    systemctl is-active --quiet np1-control-uplink.service
    test -x "$BASE/experimental/ncc-native-wifi-sta-helper"
    test ! -e "$BASE/private/wifi/pending.json"
fi
BACKUP="$BASE/recovery/web-worker-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -m 0700 "$BACKUP"
cp -p "$BASE/bin/ncc" "$BACKUP/ncc"
capture(){
    systemctl show np1-control-ap.service np1-control-modem.service np1-control-native.service -p Id -p MainPID -p InvocationID -p ActiveState -p SubState
    if test "$UPDATE_UPLINK" = 1; then
        systemctl show app-@Communication.service app-@DriveRec.service app-@ImageRecog.service app-@Voice.service qmmf-server.service -p Id -p MainPID -p InvocationID -p ActiveState -p SubState
    fi
}
capture > "$BACKUP/workers-before.txt"
COMPLETED=0
LED_WAS_ACTIVE=0
if systemctl is-active --quiet np1-control-led.service; then LED_WAS_ACTIVE=1; fi
restore_led(){
    # The LED unit Requires the broker, so restarting the broker stops it.
    # Resume only an originally active owner worker; keep saved settings intact.
    if test "$LED_WAS_ACTIVE" = 1; then systemctl start np1-control-led.service; fi
}
finish(){
    result=$?
    trap - 0
    if test "$COMPLETED" != 1; then
        cp "$BACKUP/ncc" "$BASE/bin/ncc.rollback"
        chmod 0755 "$BASE/bin/ncc.rollback"
        mv -f "$BASE/bin/ncc.rollback" "$BASE/bin/ncc"
        systemctl restart np1-control-broker.service np1-control.service || result=1
        if test "$UPDATE_UPLINK" = 1; then systemctl restart np1-control-uplink.service || result=1; fi
        restore_led || result=1
        echo 'Web update failed; previous owner binary restored' >&2
    fi
    exit "$result"
}
trap finish 0
cp bin/ncc "$BASE/bin/ncc.web-new"
chmod 0755 "$BASE/bin/ncc.web-new"
mv -f "$BASE/bin/ncc.web-new" "$BASE/bin/ncc"
systemctl restart np1-control-broker.service
systemctl restart np1-control.service
if test "$UPDATE_UPLINK" = 1; then
    systemctl restart np1-control-uplink.service
    systemctl is-active --quiet np1-control-uplink.service
    expected=$(sha256sum bin/ncc | awk '{print $1}')
    for file in "$BASE/bin/ncc" /run/np1-control/ncc /run/np1-control-uplink/ncc; do
        test "$(sha256sum "$file" | awk '{print $1}')" = "$expected"
    done
fi
restore_led
sh tools/verify.sh
capture > "$BACKUP/workers-after.txt"
cmp "$BACKUP/workers-before.txt" "$BACKUP/workers-after.txt"
COMPLETED=1
echo 'Web/broker updated; AP, modem and native worker identities preserved'

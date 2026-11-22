#!/bin/sh
set -eu
umask 077

BASE=/overlay/owner-apps/np1-control
UPLINK=$BASE/private/uplink
INSTALLED=/run/np1-control-uplink/helper
HELPER=$INSTALLED
STOCK_MAP=/tmp/victoria/victoria_PMEM_AL_SRV_SET_SETTING_ITEM.map
MODE=${1:-}
case "$MODE:$#" in
    inspect:1|snapshot:1) ;;
    diagnostic:2)
        case "$2" in ''|*[!a-f0-9]* ) exit 64 ;; esac
        test "${#2}" = 64
        HELPER=$(dirname "$0")/helper
        test -f "$HELPER" && test ! -L "$HELPER"
        test "$(sha256sum "$HELPER" | cut -d ' ' -f 1)" = "$2"
        ;;
    *) echo 'Usage: uplink-recovery.sh inspect | diagnostic SHA256 | snapshot' >&2; exit 64 ;;
esac
test "$(id -u)" = 0
test -x "$HELPER"
cmp -s "$INSTALLED" "$BASE/experimental/ncc-native-wifi-sta-helper"
test -f "$UPLINK/wifi.lock" && test ! -L "$UPLINK/wifi.lock"
exec 9<"$UPLINK/wifi.lock"
attempt=0
until flock -xn 9; do
    attempt=$((attempt + 1))
    test "$attempt" -lt 22 || exit 75
    sleep 1
done
test ! -e "$BASE/private/wifi/pending.json"
test ! -e "$UPLINK/wifi-use.json"

OWNER=$(pidof Communication)
case "$OWNER" in ''|*[!0-9]*) exit 75 ;; esac
test "$(readlink "/proc/$OWNER/exe")" = /usr/local/bin/Communication

LOG=$(mktemp -d /run/np1-uplink-recovery.XXXXXX)
printf 'Private diagnostics: %s\n' "$LOG"

setting() {
    od -An -j332 -N4 -tu4 "$STOCK_MAP" | tr -d ' \n'
}

invoke() {
    name=$1
    shift
    if env PATH=/usr/bin:/bin LD_LIBRARY_PATH=/usr/local/lib NCC_NATIVE_WIFI_STA_TRIAL=1 \
        "$HELPER" "$@" >"$LOG/$name.out" 2>"$LOG/$name.err"; then
        return 0
    else
        result=$?
        printf '%s failed (exit=%s)\n' "$name" "$result" >&2
        grep -E '^STA read-back stage=STA-(get|readback) owner=[012] native40=([01]|unavailable)$' "$LOG/$name.err" >&2 || true
        tail -n 1 "$LOG/$name.err" >&2
        return "$result"
    fi
}

verify() {
    name=$1
    wire=$2
    native=$3
    grep -qx "wifi-sta-wire=$wire" "$LOG/$name.out"
    grep -qx "native-40=$native" "$LOG/$name.out"
    grep -qx 'cleanup=matched; own-receiver-removed' "$LOG/$name.out"
    test "$(setting)" = "$native"
}

invoke preflight --preflight
case "$(setting)" in
    0) before=0; wire=1 ;;
    1) before=1; wire=2 ;;
    *) exit 78 ;;
esac
printf '%s\n' "$before" >"$LOG/native-before"
printf 'STA native before: %s\n' "$before"
if test "$MODE" = snapshot; then
    systemctl show -p Id -p MainPID -p ActiveState app-@Communication.service \
        app-@DriveRec.service app-@ImageRecog.service app-@Voice.service qmmf-server.service >"$LOG/stock-before"
    cp -p "$UPLINK/settings.json" "$LOG/uplink-settings-before"
    echo 'Recovery snapshot ready'
else
    invoke read --get "$OWNER"
    verify read "$wire" "$before"
    echo 'STA owner and native read-back matched'
fi

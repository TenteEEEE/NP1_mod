#!/bin/sh
set -eu
umask 077

BASE=/overlay/owner-apps/np1-control
STAGE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
UNIT_NAME=np1-control-uplink.service
UNIT=/etc/systemd/system/$UNIT_NAME
HELPER=experimental/ncc-native-wifi-sta-helper
EVENT=tools/ncc-wifi-dhcp-event.sh
FILES="bin/ncc $HELPER $EVENT"
RUN=/run/np1-control-uplink
case "$STAGE" in /run/np1-uplink-update.??????) ;; *) exit 64 ;; esac
test "$(id -u)" = 0
test "$(stat -c '%u:%a' "$STAGE")" = 0:700
test ! -f "$BASE/recovery/owner-recovery-install"
test "$(systemctl show "$UNIT_NAME" -p FragmentPath --value)" = "$UNIT"
test "$UNIT" -ef "/overlay$UNIT"
test -f "$UNIT" && test ! -L "$UNIT"
test -z "$(systemctl show "$UNIT_NAME" -p DropInPaths --value)"
test ! -e "$BASE/private/wifi/pending.json"
test ! -e "$BASE/private/uplink/wifi-use.json"
. "$STAGE/tools/update-lock.sh"
cd "$STAGE"
sha256sum -c SHA256SUMS >/dev/null

capture() {
    systemctl show app-@Communication.service app-@DriveRec.service app-@ImageRecog.service \
        app-@Voice.service qmmf-server.service np1-control-native.service np1-control-ap.service \
        np1-control-modem.service np1-control-tailscale.service -p Id -p MainPID -p ActiveState -p InvocationID
}

atomic_install() {
    source=$1
    destination=$2
    mode=$3
    test -f "$source" && test ! -L "$source" && test ! -L "$destination" || return 1
    temporary=$(mktemp "$destination.ncc-new.XXXXXX") || return 1
    if ! cp -p "$source" "$temporary" || ! chmod "$mode" "$temporary" || ! mv -f "$temporary" "$destination"; then
        rm -f -- "$temporary"
        return 1
    fi
    cmp -s "$source" "$destination"
}

restart_owner() {
    systemctl daemon-reload || return 1
    systemctl restart np1-control-broker.service || return 1
    systemctl restart np1-control.service || return 1
    systemctl restart np1-control-ota-guard.service || return 1
    systemctl restart "$UNIT_NAME" || return 1
    if test -f "$BACKUP/led-active"; then systemctl start np1-control-led.service || return 1; fi
    cmp -s "$BASE/bin/ncc" /run/np1-control/ncc && cmp -s "$BASE/bin/ncc" "$RUN/ncc"
}

restore_files() {
    atomic_install "$BACKUP/bin/ncc" "$BASE/bin/ncc" "$(stat -c %a "$BACKUP/bin/ncc")" || return 1
    atomic_install "$BACKUP/$HELPER" "$BASE/$HELPER" "$(stat -c %a "$BACKUP/$HELPER")" || return 1
    atomic_install "$BACKUP/uplink.service" "$UNIT" "$(stat -c %a "$BACKUP/uplink.service")" || return 1
    if test -f "$BACKUP/$EVENT"; then
        atomic_install "$BACKUP/$EVENT" "$BASE/$EVENT" "$(stat -c %a "$BACKUP/$EVENT")" || return 1
    elif test -e "$BASE/$EVENT"; then
        test -f "$BACKUP/event-absent" && cmp -s "$EVENT" "$BASE/$EVENT" || return 1
        rm -- "$BASE/$EVENT" || return 1
    fi
    sync
    restart_owner || return 1
    cmp -s "$BACKUP/$HELPER" "$RUN/helper"
}

verify_internet() {
    attempts=0
    while test "$attempts" -lt 18; do
        if "$RUN/ncc" --mode cellular-internet-probe 2>/dev/null | grep -q '"status":"passed"'; then
            echo 'Bound cellular DNS and HTTPS verified'
            return 0
        fi
        if grep -q '"wifiInternet":"verified"' "$RUN/status.json" 2>/dev/null &&
            ip -4 route get 1.1.1.1 | grep -q ' dev wlan0 '; then
            echo 'Bound Wi-Fi DNS and HTTPS verified'
            return 0
        fi
        attempts=$((attempts + 1))
        sleep 3
    done
    return 1
}

case "${1:-}" in
    apply)
        test "$#" = 2
        expected=$2
        case "$expected" in ''|*[!0-9.]*) exit 64 ;; esac
        for target in bin/ncc "$HELPER" "$EVENT" "systemd/$UNIT_NAME"; do
            test -f "$target" && test ! -L "$target"
        done
        chmod 0755 bin/ncc
        test "$(bin/ncc --mode update-version)" = "$expected"
        bin/ncc --mode self-test
        systemctl is-active --quiet "$UNIT_NAME"
        BACKUP=$(mktemp -d "$BASE/recovery/uplink-worker.XXXXXX")
        echo "Recovery backup: $BACKUP"
        for entry in $FILES; do
            mkdir -p "$BACKUP/$(dirname "$entry")"
            test ! -L "$BASE/$entry"
            if test -e "$BASE/$entry"; then
                test -f "$BASE/$entry"
                cp -p "$BASE/$entry" "$BACKUP/$entry"
            else
                test "$entry" = "$EVENT"
                : > "$BACKUP/event-absent"
            fi
        done
        cp -p "$UNIT" "$BACKUP/uplink.service"
        capture > "$BACKUP/services-before"
        if systemctl is-active --quiet np1-control-led.service; then : > "$BACKUP/led-active"; fi
        sha256sum bin/ncc "$HELPER" "$EVENT" "systemd/$UNIT_NAME" > "$BACKUP/installed.sha256"
        sync
        complete=0
        finish() {
            result=$?
            trap - 0
            trap '' HUP INT TERM
            if test "$complete" = 0; then
                if restore_files; then echo 'Update failed; previous software restored'; else echo 'Automatic rollback could not be verified' >&2; fi
                result=1
            fi
            printf '%s\n' "$result" > "$STAGE/result"
            exit "$result"
        }
        trap finish 0
        trap 'exit 129' HUP
        trap 'exit 130' INT
        trap 'exit 143' TERM
        atomic_install bin/ncc "$BASE/bin/ncc" 0755
        atomic_install "$HELPER" "$BASE/$HELPER" 0700
        atomic_install "$EVENT" "$BASE/$EVENT" 0644
        atomic_install "systemd/$UNIT_NAME" "$UNIT" 0644
        sync
        test "$UNIT" -ef "/overlay$UNIT"
        restart_owner
        cmp -s "$HELPER" "$RUN/helper"
        cmp -s "$EVENT" "$RUN/dhcp-event"
        test "$(/run/np1-control/ncc --mode update-version)" = "$expected"
        test "$("$RUN/ncc" --mode update-version)" = "$expected"
        sh tools/verify.sh
        verify_internet
        capture > "$BACKUP/services-after"
        cmp -s "$BACKUP/services-before" "$BACKUP/services-after"
        complete=1
        echo "NP1CC $expected installed and verified"
        ;;
    rollback)
        test "$#" = 2
        case "$2" in uplink-worker.??????) ;; *) exit 64 ;; esac
        BACKUP=$BASE/recovery/$2
        test "$(stat -c '%u:%a' "$BACKUP")" = 0:700
        test ! -L "$BACKUP"
        sha256sum -c "$BACKUP/installed.sha256" >/dev/null
        for entry in $FILES; do cmp -s "$entry" "$BASE/$entry"; done
        cmp -s "systemd/$UNIT_NAME" "$UNIT"
        restore_files
        echo 'Previous NP1CC and uplink files restored'
        ;;
    *) exit 64 ;;
esac

#!/bin/sh
set -eu
umask 077

BASE=/overlay/owner-apps/np1-control
STAGE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
BACKUP=$STAGE/before
BROKER=/run/systemd/system/np1-control-broker.service.d/90-ncc-uplink-trial.conf
UPLINK=/run/systemd/system/np1-control-uplink.service.d/90-ncc-uplink-trial.conf
case "$STAGE" in /run/np1-uplink-candidate.??????) ;; *) exit 64 ;; esac
test "$(id -u)" = 0
test "$(stat -c '%u:%a' "$STAGE")" = 0:700
cd "$STAGE"

stock() {
    systemctl show -p Id -p MainPID -p ActiveState app-@Communication.service \
        app-@DriveRec.service app-@ImageRecog.service app-@Voice.service qmmf-server.service
}

restart_owner() {
    systemctl daemon-reload
    systemctl restart np1-control-broker.service
    systemctl restart np1-control.service np1-control-uplink.service
    if test -f "$BACKUP/led-was-active"; then systemctl start np1-control-led.service; fi
    systemctl is-active --quiet np1-control-broker.service
    systemctl is-active --quiet np1-control.service
    systemctl is-active --quiet np1-control-uplink.service
}

remove_trial_units() {
    for dropin in "$BROKER" "$UPLINK"; do
        if test -f "$dropin" && grep -Fqx "# NP1 uplink field trial $STAGE" "$dropin"; then
            rm -- "$dropin"
        fi
    done
}

private_files='wifi/profiles.json uplink/settings.json uplink/state.json uplink/saved.json uplink/wifi-use.json'

case "${1:-}" in
    start)
        sha256sum -c SHA256SUMS
        test ! -e "$BROKER" && test ! -e "$UPLINK"
        test ! -e "$BASE/private/wifi/pending.json"
        chmod 0700 "$STAGE/ncc" "$STAGE/helper"
        "$STAGE/ncc" --mode self-test
        mkdir -m 0700 "$BACKUP"
        stock > "$BACKUP/stock"
        if systemctl is-active --quiet np1-control-led.service; then : > "$BACKUP/led-was-active"; fi
        for entry in $private_files; do
            target=$BASE/private/$entry
            mkdir -p "$BACKUP/$(dirname "$entry")"
            if test -e "$target"; then test -f "$target" && test ! -L "$target"; cp -p "$target" "$BACKUP/$entry"; fi
        done
        complete=0
        trap 'result=$?; if test "$complete" = 0; then remove_trial_units; restart_owner || result=1; fi; exit "$result"' 0
        mkdir -p "$(dirname "$BROKER")" "$(dirname "$UPLINK")"
        printf '# NP1 uplink field trial %s\n[Service]\nExecStartPre=/bin/cp %s/ncc /run/np1-control/ncc\nExecStartPre=/bin/chmod 0755 /run/np1-control/ncc\n' "$STAGE" "$STAGE" > "$BROKER"
        printf '# NP1 uplink field trial %s\n[Service]\nExecStartPre=/bin/cp %s/ncc /run/np1-control-uplink/ncc\nExecStartPre=/bin/cp %s/helper /run/np1-control-uplink/helper\nExecStartPre=/bin/cp %s/dhcp-event /run/np1-control-uplink/dhcp-event\nExecStartPre=/bin/chmod 0700 /run/np1-control-uplink/ncc /run/np1-control-uplink/helper /run/np1-control-uplink/dhcp-event\nCapabilityBoundingSet=CAP_NET_RAW CAP_NET_ADMIN\n' "$STAGE" "$STAGE" "$STAGE" "$STAGE" > "$UPLINK"
        restart_owner
        cmp -s "$STAGE/ncc" /run/np1-control/ncc
        cmp -s "$STAGE/ncc" /run/np1-control-uplink/ncc
        cmp -s "$STAGE/helper" /run/np1-control-uplink/helper
        cmp -s "$STAGE/dhcp-event" /run/np1-control-uplink/dhcp-event
        stock | cmp -s "$BACKUP/stock" -
        complete=1
        echo 'Candidate loaded; stock identities unchanged'
        ;;
    restore)
        test -f "$BACKUP/stock"
        test ! -e "$BASE/private/wifi/pending.json"
        systemctl stop np1-control-uplink.service np1-control.service np1-control-broker.service
        remove_trial_units
        for entry in $private_files; do
            target=$BASE/private/$entry
            test ! -L "$target"
            if test -f "$BACKUP/$entry"; then
                cp -p "$BACKUP/$entry" "$target.uplink-trial-restore"
                mv -f "$target.uplink-trial-restore" "$target"
                cmp -s "$BACKUP/$entry" "$target"
            else
                rm -f -- "$target"
            fi
        done
        restart_owner
        cmp -s "$BASE/bin/ncc" /run/np1-control/ncc
        cmp -s "$BASE/experimental/ncc-native-wifi-sta-helper" /run/np1-control-uplink/helper
        stock | cmp -s "$BACKUP/stock" -
        echo 'Owner services and original settings restored; stock identities unchanged'
        ;;
    *) exit 64 ;;
esac

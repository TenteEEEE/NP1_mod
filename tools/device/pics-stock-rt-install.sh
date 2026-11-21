#!/bin/sh
# Install or restore the NCC-owned early post-boot block and its helper.
set -eu
umask 077

base=/overlay/owner-apps/np1-control
helper=$base/tools/pics-stock-rt.sh
recovery=$base/recovery/pics-rt
upper=/overlay/etc/init.post_boot.sh
live=/etc/init.post_boot.sh
marker='# BEGIN NP1_CONTROL_CENTER_PICS_RT_V1'
tools=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)

test "$(id -u)" = 0
test "$(uname -m)" = aarch64
test "$(uname -r)" = 4.14.206-perf
grep -Eq ' /etc overlay [^ ]*upperdir=/overlay/etc(,| )' /proc/mounts
test -d "$base/tools"
test ! -L "$helper" && test ! -L "$recovery"

restore() {
    test -d "$recovery"
    action=remove-pics-rt
    if grep -qxF "$marker" "$recovery/init.post_boot.upper.sh"; then action=add-pics-rt; fi
    python3 "$tools/postboot.py" "$action"
    if test -f "$recovery/pics-stock-rt.sh"; then cp -p "$recovery/pics-stock-rt.sh" "$helper"; else rm -f "$helper"; fi
    sh -n "$live"
    sh -n "$upper"
    rm -f "$recovery/pics-stock-rt.sh" "$recovery/init.post_boot.sh" "$recovery/init.post_boot.upper.sh"
    rmdir "$recovery"
    echo 'Original owner boot block restored; realtime CPU budgets are recreated on reboot.'
}

install_helper() {
    temporary=$(mktemp "$helper.new.XXXXXX")
    if ! (install -m 0700 "$tools/pics-stock-rt.sh" "$temporary" && mv -f "$temporary" "$helper"); then
        rm -f "$temporary"
        return 1
    fi
    cmp "$tools/pics-stock-rt.sh" "$helper"
}

case "${1:-plan}" in
    plan)
        printf 'Install %s and add its marked block at the start of post-boot.\n' "$helper"
        printf 'Backup and restore files under %s.\n' "$recovery"
        ;;
    apply)
        sh -n "$tools/pics-stock-rt.sh"
        if cmp -s "$tools/pics-stock-rt.sh" "$helper" && grep -qxF "$marker" "$upper" && grep -qxF "$marker" "$live"; then
            python3 "$tools/postboot.py" add-pics-rt
            echo 'Stock Bluetooth boot helper already installed.'
            exit 0
        fi
        if test -f "$recovery/init.post_boot.upper.sh" && test -f "$recovery/init.post_boot.sh"; then
            python3 "$tools/postboot.py" add-pics-rt
            install_helper
            echo 'Stock Bluetooth boot helper updated and verified.'
            exit 0
        fi
        mkdir -m 0700 "$recovery"
        cp -p "$upper" "$recovery/init.post_boot.upper.sh"
        cp -p "$live" "$recovery/init.post_boot.sh"
        if test -f "$helper"; then cp -p "$helper" "$recovery/pics-stock-rt.sh"; fi
        trap 'code=$?; if test "$code" -ne 0; then restore; fi' 0
        install_helper
        python3 "$tools/postboot.py" add-pics-rt
        for path in "$upper" "$live"; do
            sh -n "$path"
            test "$(sed -n '2p' "$path")" = "$marker"
        done
        echo 'Stock Bluetooth boot helper installed and verified.'
        ;;
    rollback) restore ;;
    *) exit 64 ;;
esac

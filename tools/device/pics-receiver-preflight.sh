#!/bin/sh
# Read-only NP1 Bluetooth inventory. No controller open, scan or stock IPC calls.
set -eu

controllers=0
for node in /sys/class/bluetooth/hci[0-9]*; do
    [ -e "$node" ] || continue
    controllers=$((controllers + 1))
done
printf 'kernel_hci_controllers=%s\n' "$controllers"

if command -v busctl >/dev/null 2>&1 && busctl --no-pager status org.bluez >/dev/null 2>&1; then
    printf 'bluez_owner=present\n'
else
    printf 'bluez_owner=absent\n'
fi

watcher=$(pidof NEventWatcherCom 2>/dev/null || true)
set -- $watcher
if [ "$#" -eq 1 ] && [ -r "/proc/$1/maps" ]; then
    if grep -q '/libAnwManagerMA\.so' "/proc/$1/maps" && grep -q '/libanwbtif\.so' "/proc/$1/maps"; then
        printf 'stock_event_watcher_ble_stack=mapped\n'
    else
        printf 'stock_event_watcher_ble_stack=not-mapped\n'
    fi
    if ls -l "/proc/$1/fd" 2>/dev/null | grep -q '/dev/ttyHS[0-9]'; then
        printf 'stock_event_watcher_serial_controller=open\n'
    else
        printf 'stock_event_watcher_serial_controller=not-observed\n'
    fi
else
    printf 'stock_event_watcher_ble_stack=unavailable\n'
fi

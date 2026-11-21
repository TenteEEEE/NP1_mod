#!/bin/sh
# Reserve the stock controller's realtime CPU time before Communication starts it.
set -eu

cpu=/sys/fs/cgroup/cpu,cpuacct
group=/system.slice/NEventWatcherCom.service
parent=$cpu/system.slice/cpu.rt_runtime_us
service=$cpu$group/cpu.rt_runtime_us
budget=20000

fail() { printf 'Stock Bluetooth realtime CPU budget: %s\n' "$1" >&2; exit 1; }

stock_group_ready() {
    reason='stock CPU group is unavailable'
    test -f "$service" || return 1
    reason='stock service did not report a main process'
    pid=$(systemctl show -p MainPID --value NEventWatcherCom.service) || return 1
    case "$pid" in ''|*[!0-9]*) return 1 ;; esac
    test "$pid" -gt 1 || return 1
    reason='stock process name or CPU group does not match'
    # /proc/PID/exe is protected across the vendor service's UID boundary.
    # Verify the genuine systemd unit, its main task name, and the CPU group.
    test "$(cat /proc/"$pid"/comm)" = NEventWatcherCo &&
        grep -qx "[0-9]*:cpu,cpuacct:$group" /proc/"$pid"/cgroup
}

test "$(id -u)" = 0 || fail 'root is required'
attempt=0
until stock_group_ready; do
    attempt=$((attempt + 1))
    test "$attempt" -lt 30 || fail "$reason"
    sleep 0.2
done
systemctl show -p ExecStart --value NEventWatcherCom.service | grep -Fq 'path=/usr/local/bin/NEventWatcherCom ;' || fail 'unexpected stock executable'
for directory in "$cpu" "$cpu/system.slice" "$cpu$group"; do
    test "$(cat "$directory/cpu.rt_period_us")" -eq 1000000 || fail 'unexpected realtime period'
done
test "$(cat "$cpu/cpu.rt_runtime_us")" -ge "$budget" || fail 'insufficient root CPU budget'

parent_before=$(cat "$parent")
service_before=$(cat "$service")
if test "$service_before" -ge "$budget"; then
    printf 'Stock Bluetooth realtime CPU budget already set: %s us\n' "$service_before"
    exit 0
fi
test "$service_before" -eq 0 || fail 'unexpected stock CPU budget'
if test "$parent_before" -eq 0; then
    printf '%s\n' "$budget" > "$parent"
else
    test "$parent_before" -ge "$budget" || fail 'insufficient system CPU budget'
fi
if ! (printf '%s\n' "$budget" > "$service"); then
    if test "$parent_before" -eq 0; then printf '0\n' > "$parent"; fi
    fail 'stock CPU budget write failed'
fi
test "$(cat "$parent")" -ge "$budget"
test "$(cat "$service")" -eq "$budget"
printf 'Reserved %s us per second for stock Bluetooth\n' "$budget"

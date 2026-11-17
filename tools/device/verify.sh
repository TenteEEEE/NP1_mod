#!/bin/sh
set -eu
systemctl is-active np1-control-broker.service np1-control.service
PID=$(systemctl show np1-control.service -p MainPID --value)
test "$PID" -gt 1
BASE=/overlay/owner-apps/np1-control
if test -f "$BASE/recovery/owner-recovery-install"; then
    worker_uid=$(cat "$BASE/worker-uid")
else
    worker_uid=$(id -u ncc)
fi
test "$worker_uid" -gt 0
test "$(awk '/^Uid:/ {print $2}' /proc/"$PID"/status)" = "$worker_uid"
if test -f "$BASE/recovery/owner-recovery-install"; then
    awk -v uid="$worker_uid" '
        /^Uid:|^Gid:/ { if ($2 != uid || $3 != uid || $4 != uid || $5 != uid) exit 1 }
        /^Groups:/ { if (NF != 1) exit 1 }
        /^Cap(Eff|Bnd|Amb):/ { if ($2 !~ /^0*400$/) exit 1; caps++ }
        END { if (caps != 3) exit 1 }
    ' /proc/"$PID"/status
fi
test -S /run/np1-control/broker.sock
python3 - <<'PY'
import ssl
import urllib.request
import urllib.error
ctx = ssl._create_unverified_context()
for url in ('http://127.0.0.1/api/v1/auth/session', 'https://127.0.0.1/api/v1/auth/session'):
    with urllib.request.urlopen(url, context=ctx, timeout=10) as response:
        if response.code != 200:
            raise RuntimeError('Password-free session failed')
with urllib.request.urlopen('https://127.0.0.1/', context=ctx, timeout=10) as response:
    if b'NP1 Control Center' not in response.read():
        raise RuntimeError('UI shell missing')
print('HTTP/HTTPS, password-free session and UI shell verified')
PY
systemctl show np1-control.service -p NRestarts -p Result -p ExecMainStatus
sh -n /etc/init.post_boot.sh
sh -n /overlay/etc/init.post_boot.sh
if test -f "$BASE/recovery/owner-recovery-install"; then
    wants=$(systemctl show np1-owner-recovery.service -p Wants --value)
    for file in "$BASE"/units/*; do
        unit=${file##*/}
        case "$unit" in np1-control-led.service|np1-control-uplink.service) continue ;; esac
        case " $wants " in *" $unit "*) ;; *) echo "Missing owner boot dependency: $unit" >&2; exit 1 ;; esac
    done
else
    grep -q '^# BEGIN NP1_CONTROL_CENTER_V1$' /overlay/etc/init.post_boot.sh
fi
if test -f /etc/systemd/system/np1-control-plate.service; then
    for i in 1 2 3 4 5 6 7 8 9 10; do test -S /run/np1-control-plate/sensor.sock && break; sleep 1; done
    systemctl is-active np1-control-plate.service
    test -S /run/np1-control-plate/sensor.sock
    if test ! -f "$BASE/recovery/owner-recovery-install"; then
        grep -q '^# BEGIN NP1_CONTROL_CENTER_PLATE_V1$' /overlay/etc/init.post_boot.sh
    fi
fi

#!/bin/sh
# Update the owner Web/API and the NCC-owned DriveRec runtime hook.
# DriveRec itself is never restarted or modified. The native worker waits for
# healthy stock recording, injects the hook and rebuilds encoders in-process.
set -eu
umask 077
BASE=/overlay/owner-apps/np1-control
STAGE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
UNIT=/etc/systemd/system/np1-control-broker.service
OWNER_UNIT=$BASE/units/np1-control-broker.service
NATIVE_UNIT=/etc/systemd/system/np1-control-native.service
DROPIN_DIR=/etc/systemd/system/app-@DriveRec.service.d
DROPIN=$DROPIN_DIR/90-ncc-recorder-hook.conf
HOOK_DIR=$BASE/recorder-hook
HOOK=$HOOK_DIR/libncc-driverec-hook.so
INJECTOR=$BASE/experimental/ncc-recorder-injector
OPERATIONS=$BASE/experimental/ncc-native-operations-helper
RUNTIME_HOOK=/run/np1-control-recorder-hook/libncc-driverec-hook.so

test "$(id -u)" = 0
test -f "$UNIT"
test ! -L "$BASE" && test ! -L "$HOOK_DIR" && test ! -L "$DROPIN_DIR" && test ! -L "$DROPIN" && test ! -L "$HOOK" && test ! -L "$INJECTOR" && test ! -L "$OPERATIONS" && test ! -L "$NATIVE_UNIT"
OWNER_LAYOUT=0
if test -f "$BASE/recovery/owner-recovery-install"; then
    OWNER_LAYOUT=1
    test -f "$BASE/worker-uid" && test -f "$OWNER_UNIT"
    cmp "$OWNER_UNIT" "$UNIT"
    NCC_UID=$(cat "$BASE/worker-uid")
else
    id ncc >/dev/null 2>&1
    NCC_UID=$(id -u ncc)
fi
cd "$STAGE"
sha256sum -c SHA256SUMS >/dev/null
test -f experimental/libncc-driverec-hook.so
test -f experimental/ncc-recorder-injector
test -f experimental/ncc-native-operations-helper
test -f systemd/np1-control-native.service
/bin/chmod 0755 bin/ncc
bin/ncc --mode self-test
case "$NCC_UID" in ''|*[!0-9]*) exit 1 ;; esac
test "$NCC_UID" -gt 0
DRIVE_REC_PID=$(systemctl show app-@DriveRec.service -p MainPID --value)
case "$DRIVE_REC_PID" in ''|*[!0-9]*) exit 1 ;; esac
MAPPED_HOOK_BEFORE=0
if grep -Fq "$RUNTIME_HOOK" "/proc/$DRIVE_REC_PID/maps"; then MAPPED_HOOK_BEFORE=1; fi

BACKUP=$(mktemp -d "$BASE/recovery/recorder-hook-XXXXXXXX")
cp -p "$BASE/bin/ncc" "$BACKUP/ncc"
cp -p "$UNIT" "$BACKUP/np1-control-broker.service"
cp -p "$NATIVE_UNIT" "$BACKUP/np1-control-native.service"
if test "$OWNER_LAYOUT" = 1; then cp -p "$OWNER_UNIT" "$BACKUP/owner-np1-control-broker.service"; fi
if test -f "$DROPIN"; then cp -p "$DROPIN" "$BACKUP/90-ncc-recorder-hook.conf"; else touch "$BACKUP/dropin.absent"; fi
if test -f "$HOOK"; then cp -p "$HOOK" "$BACKUP/libncc-driverec-hook.so"; else touch "$BACKUP/hook.absent"; fi
if test -f "$INJECTOR"; then cp -p "$INJECTOR" "$BACKUP/ncc-recorder-injector"; else touch "$BACKUP/injector.absent"; fi
if test -f "$OPERATIONS"; then cp -p "$OPERATIONS" "$BACKUP/ncc-native-operations-helper"; else touch "$BACKUP/operations.absent"; fi

capture_stock() {
    systemctl show app-@Communication.service app-@DriveRec.service app-@ImageRecog.service app-@Voice.service qmmf-server.service NEventWatcherCom.service -p Id -p MainPID -p InvocationID -p ActiveState -p SubState -p Result -p NRestarts
}
capture_owner() {
    systemctl show np1-control-ap.service np1-control-modem.service np1-control-native.service np1-control-plate.service -p Id -p MainPID -p InvocationID -p ActiveState -p SubState
}
capture_stock > "$BACKUP/stock-before.txt"
capture_owner > "$BACKUP/owner-before.txt"
LED_WAS_ACTIVE=0
if systemctl is-active --quiet np1-control-led.service; then LED_WAS_ACTIVE=1; fi
restore_led() { if test "$LED_WAS_ACTIVE" = 1; then systemctl start np1-control-led.service; fi; }
restore_file() {
    source=$1
    target=$2
    cp -p "$source" "$target.restore"
    mv -f "$target.restore" "$target"
}
COMPLETED=0
finish() {
    result=$?
    trap - 0
    if test "$COMPLETED" != 1; then
        restore_file "$BACKUP/ncc" "$BASE/bin/ncc" || result=1
        restore_file "$BACKUP/np1-control-broker.service" "$UNIT" || result=1
        restore_file "$BACKUP/np1-control-native.service" "$NATIVE_UNIT" || result=1
        if test "$OWNER_LAYOUT" = 1; then restore_file "$BACKUP/owner-np1-control-broker.service" "$OWNER_UNIT" || result=1; fi
        if test -f "$BACKUP/90-ncc-recorder-hook.conf"; then restore_file "$BACKUP/90-ncc-recorder-hook.conf" "$DROPIN" || result=1; elif test -f "$BACKUP/dropin.absent"; then rm -f "$DROPIN" || result=1; fi
        if test -f "$BACKUP/libncc-driverec-hook.so"; then restore_file "$BACKUP/libncc-driverec-hook.so" "$HOOK" || result=1; elif test -f "$BACKUP/hook.absent"; then rm -f "$HOOK" || result=1; fi
        if test -f "$BACKUP/ncc-recorder-injector"; then restore_file "$BACKUP/ncc-recorder-injector" "$INJECTOR" || result=1; elif test -f "$BACKUP/injector.absent"; then rm -f "$INJECTOR" || result=1; fi
        if test -f "$BACKUP/ncc-native-operations-helper"; then restore_file "$BACKUP/ncc-native-operations-helper" "$OPERATIONS" || result=1; elif test -f "$BACKUP/operations.absent"; then rm -f "$OPERATIONS" || result=1; fi
        systemctl daemon-reload || result=1
        systemctl restart np1-control-native.service np1-control-broker.service np1-control.service || result=1
        restore_led || result=1
        echo 'Recorder hook update failed; owner binary, broker unit, hook and drop-in restored' >&2
    fi
    exit "$result"
}
trap finish 0

restore_file bin/ncc "$BASE/bin/ncc"
mkdir -p -m 0755 "$HOOK_DIR"
chmod 0755 "$HOOK_DIR"
restore_file experimental/libncc-driverec-hook.so "$HOOK"
chmod 0555 "$HOOK"
mkdir -p -m 0700 "$BASE/experimental"
restore_file experimental/ncc-recorder-injector "$INJECTOR"
chmod 0600 "$INJECTOR"
restore_file experimental/ncc-native-operations-helper "$OPERATIONS"
chmod 0600 "$OPERATIONS"
rm -f "$DROPIN"
restore_file systemd/np1-control-native.service "$NATIVE_UNIT"
chmod 0644 "$NATIVE_UNIT"

BROKER_NEW=$BACKUP/np1-control-broker.service.new
if test "$OWNER_LAYOUT" = 1; then
    sed -e 's/^Group=ncc$/Group=root/' -e "s/@NCC_UID@/$NCC_UID/g" systemd/np1-control-broker.service > "$BROKER_NEW"
    sed -i "/^RuntimeDirectoryMode=/a ExecStartPre=+/bin/chown root:$NCC_UID /run/np1-control\nExecStartPre=+/bin/chmod g+s /run/np1-control" "$BROKER_NEW"
else
    sed "s/@NCC_UID@/$NCC_UID/g" systemd/np1-control-broker.service > "$BROKER_NEW"
fi
chmod 0644 "$BROKER_NEW"
if test "$OWNER_LAYOUT" = 1; then restore_file "$BROKER_NEW" "$OWNER_UNIT"; fi
restore_file "$BROKER_NEW" "$UNIT"

systemctl daemon-reload
cmp experimental/libncc-driverec-hook.so "$HOOK"
cmp experimental/ncc-recorder-injector "$INJECTOR"
cmp experimental/ncc-native-operations-helper "$OPERATIONS"
cmp systemd/np1-control-native.service "$NATIVE_UNIT"
test ! -e "$DROPIN"
! systemctl show app-@DriveRec.service -p Environment --value | grep -Fq "LD_PRELOAD="
if test "$MAPPED_HOOK_BEFORE" = 0; then systemctl restart np1-control-native.service; fi
systemctl restart np1-control-broker.service
systemctl restart np1-control.service
restore_led
sh tools/verify.sh
capture_stock > "$BACKUP/stock-after.txt"
capture_owner > "$BACKUP/owner-after.txt"
cmp "$BACKUP/stock-before.txt" "$BACKUP/stock-after.txt"
systemctl is-active --quiet np1-control-native.service np1-control-broker.service np1-control.service
if test "$MAPPED_HOOK_BEFORE" = 0; then
python3 - <<'PY'
import time, urllib.request
url='http://127.0.0.1/api/v1/device/recorder-advanced'
last=None
for _ in range(30):
    try:
        with urllib.request.urlopen(url, timeout=5) as response:
            last=response.read(16384).decode('utf-8')
        compact=''.join(last.split())
        if '"applied":true' in compact and '"hookLoaded":true' in compact and '"restartRequired":false' in compact:
            break
    except Exception as error:
        last={'error':str(error)}
    time.sleep(2)
else:
    raise RuntimeError('Recorder settings were not verified after runtime apply: %r' % (last,))
print('Recorder runtime settings and health generation verified')
PY
else
    echo 'Existing DriveRec hook mapping preserved; device reboot is required to replace and verify this generation'
fi
expected=$(sha256sum bin/ncc | awk '{print $1}')
for file in "$BASE/bin/ncc" /run/np1-control/ncc; do test "$(sha256sum "$file" | awk '{print $1}')" = "$expected"; done
COMPLETED=1
if test "$MAPPED_HOOK_BEFORE" = 0; then
    echo "Recorder hook applied and recording verified without restarting DriveRec; backup: $BACKUP"
else
    echo "Recorder runtime hook staged without restarting DriveRec; reboot required; backup: $BACKUP"
fi

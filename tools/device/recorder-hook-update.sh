#!/bin/sh
# Update the owner Web/API, broker unit and the NCC-owned DriveRec startup hook.
# DriveRec itself is not restarted; the owner applies settings explicitly in Web UI.
set -eu
umask 077
BASE=/overlay/owner-apps/np1-control
STAGE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
UNIT=/etc/systemd/system/np1-control-broker.service
OWNER_UNIT=$BASE/units/np1-control-broker.service
DROPIN_DIR=/etc/systemd/system/app-@DriveRec.service.d
DROPIN=$DROPIN_DIR/90-ncc-recorder-hook.conf
HOOK_DIR=$BASE/recorder-hook
HOOK=$HOOK_DIR/libncc-driverec-hook.so

test "$(id -u)" = 0
test -f "$BASE/recovery/owner-recovery-install"
test -f "$BASE/worker-uid" && test -f "$UNIT" && test -f "$OWNER_UNIT"
test ! -L "$BASE" && test ! -L "$HOOK_DIR" && test ! -L "$DROPIN_DIR" && test ! -L "$DROPIN" && test ! -L "$HOOK"
cmp "$OWNER_UNIT" "$UNIT"
cd "$STAGE"
sha256sum -c SHA256SUMS >/dev/null
test -f experimental/libncc-driverec-hook.so
/bin/chmod 0755 bin/ncc
bin/ncc --mode self-test
NCC_UID=$(cat "$BASE/worker-uid")
case "$NCC_UID" in ''|*[!0-9]*) exit 1 ;; esac
test "$NCC_UID" -gt 0

BACKUP=$(mktemp -d "$BASE/recovery/recorder-hook-XXXXXXXX")
cp -p "$BASE/bin/ncc" "$BACKUP/ncc"
cp -p "$UNIT" "$BACKUP/np1-control-broker.service"
cp -p "$OWNER_UNIT" "$BACKUP/owner-np1-control-broker.service"
if test -f "$DROPIN"; then cp -p "$DROPIN" "$BACKUP/90-ncc-recorder-hook.conf"; else touch "$BACKUP/dropin.absent"; fi
if test -f "$HOOK"; then cp -p "$HOOK" "$BACKUP/libncc-driverec-hook.so"; else touch "$BACKUP/hook.absent"; fi

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
        restore_file "$BACKUP/owner-np1-control-broker.service" "$OWNER_UNIT" || result=1
        if test -f "$BACKUP/90-ncc-recorder-hook.conf"; then restore_file "$BACKUP/90-ncc-recorder-hook.conf" "$DROPIN" || result=1; elif test -f "$BACKUP/dropin.absent"; then rm -f "$DROPIN" || result=1; fi
        if test -f "$BACKUP/libncc-driverec-hook.so"; then restore_file "$BACKUP/libncc-driverec-hook.so" "$HOOK" || result=1; elif test -f "$BACKUP/hook.absent"; then rm -f "$HOOK" || result=1; fi
        systemctl daemon-reload || result=1
        systemctl restart np1-control-broker.service np1-control.service || result=1
        restore_led || result=1
        echo 'Recorder hook update failed; owner binary, broker unit, hook and drop-in restored' >&2
    fi
    exit "$result"
}
trap finish 0

restore_file bin/ncc "$BASE/bin/ncc"
mkdir -p -m 0755 "$HOOK_DIR" "$DROPIN_DIR"
chmod 0755 "$HOOK_DIR" "$DROPIN_DIR"
restore_file experimental/libncc-driverec-hook.so "$HOOK"
chmod 0555 "$HOOK"
restore_file systemd/90-ncc-recorder-hook.conf "$DROPIN"
chmod 0644 "$DROPIN"

BROKER_NEW=$BACKUP/np1-control-broker.service.new
sed -e 's/^Group=ncc$/Group=root/' -e "s/@NCC_UID@/$NCC_UID/g" systemd/np1-control-broker.service > "$BROKER_NEW"
sed -i "/^RuntimeDirectoryMode=/a ExecStartPre=+/bin/chown root:$NCC_UID /run/np1-control\nExecStartPre=+/bin/chmod g+s /run/np1-control" "$BROKER_NEW"
chmod 0644 "$BROKER_NEW"
restore_file "$BROKER_NEW" "$OWNER_UNIT"
restore_file "$BROKER_NEW" "$UNIT"

systemctl daemon-reload
cmp experimental/libncc-driverec-hook.so "$HOOK"
cmp systemd/90-ncc-recorder-hook.conf "$DROPIN"
systemctl show app-@DriveRec.service -p Environment --value | grep -Fq "LD_PRELOAD=$HOOK"
systemctl restart np1-control-broker.service
systemctl restart np1-control.service
restore_led
sh tools/verify.sh
capture_stock > "$BACKUP/stock-after.txt"
capture_owner > "$BACKUP/owner-after.txt"
cmp "$BACKUP/stock-before.txt" "$BACKUP/stock-after.txt"
cmp "$BACKUP/owner-before.txt" "$BACKUP/owner-after.txt"
expected=$(sha256sum bin/ncc | awk '{print $1}')
for file in "$BASE/bin/ncc" /run/np1-control/ncc; do test "$(sha256sum "$file" | awk '{print $1}')" = "$expected"; done
COMPLETED=1
echo "Recorder hook staged without restarting DriveRec; backup: $BACKUP"

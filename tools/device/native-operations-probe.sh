#!/bin/sh
# Read-only owner requests only. Does not expose --set/--format to the launcher.
set -eu
umask 077
MODE=${1:-read-only}
case "$MODE:$#" in read-only:0|same-value:1) ;; *) exit 64;; esac
test "$(id -u)" = 0
STAGE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$STAGE"
sha256sum -c SHA256SUMS >/dev/null
exec 9>/run/np1-control-native/control.lock
flock -n 9 || exit 75
BASE=$(mktemp -d /run/np1-native-operations-probe.XXXXXXXX)
cp bin/ncc-native-operations-helper "$BASE/helper"
chmod 0700 "$BASE/helper"
UNITS='app-@Communication.service app-@DriveRec.service app-@ImageRecog.service app-@Voice.service qmmf-server.service app-@Setting.service app-@Navigation.service'
capture(){ systemctl show $UNITS -p Id -p MainPID -p InvocationID -p ActiveState -p SubState; }
capture > "$BASE/services-before.txt"
cp /pio_data/SYSTEM/SETTING/Setting.DAT "$BASE/settings-before.dat"
finish(){
 result=$?; trap - 0
 capture > "$BASE/services-after.txt"
 cmp "$BASE/services-before.txt" "$BASE/services-after.txt" || result=1
 cmp /pio_data/SYSTEM/SETTING/Setting.DAT "$BASE/settings-before.dat" || result=1
 rm -f "$BASE/helper"
 printf 'Private evidence: %s\n' "$BASE"
 printf 'NCC_EXIT_STATUS=%s\n' "$result"
 exit "$result"
}
trap finish 0
FAILED=0
for group in recorder parking installation vehicle storage alignment; do
 case "$group" in storage|alignment) owner=DriveRec;; *) owner=Setting;; esac
 pid=$(systemctl show "app-@$owner.service" -p MainPID --value)
 if ! LD_LIBRARY_PATH=/usr/local/lib NCC_NATIVE_OPERATIONS=1 "$BASE/helper" --get "$pid" "$group" > "$BASE/$group.txt" 2> "$BASE/$group-error.txt"; then
  printf '%s: unavailable; private error retained\n' "$group"
  FAILED=1
  continue
 fi
 grep -q '^cleanup=matched; own-receiver-removed$' "$BASE/$group.txt"
 printf '%s: correlated owner response and cleanup verified\n' "$group"
done
card=$(python3 - <<'PY'
import hashlib, pathlib
cards=[]
for path in pathlib.Path('/sys/class/block').glob('mmcblk[0-9]'):
    try:
        if (path/'device/type').read_text().strip() == 'SD':
            cards.append(hashlib.sha256((str(path)+':'+(path/'device/cid').read_text().strip()+':'+(path/'size').read_text().strip()).encode()).hexdigest())
    except OSError:
        pass
if len(cards)!=1:
    raise SystemExit('Unique SD identity unavailable')
print(cards[0])
PY
)
pid=$(systemctl show app-@DriveRec.service -p MainPID --value)
LD_LIBRARY_PATH=/usr/local/lib NCC_NATIVE_OPERATIONS=1 "$BASE/helper" --check-card "$pid" "$card" > "$BASE/card-check.txt" 2> "$BASE/card-check-error.txt"
grep -q '^cleanup=matched; own-receiver-removed$' "$BASE/card-check.txt"
printf 'SD card fingerprint: helper and launcher agree; no format requested\n'
if test "$MODE" = same-value; then
 for pair in 'recorder-front:recorder:1' 'recorder-cabin:recorder:2' 'recorder-audio:recorder:3' 'recorder-overlay:recorder:5' 'recorder-impact:recorder:4' 'parking-impact:parking:1' 'parking-security:parking:2' 'parking-upload:parking:3' 'vehicle-type:vehicle:1' 'collection-area:vehicle:2'; do
  id=${pair%%:*}; rest=${pair#*:}; group=${rest%:*}; field=${rest#*:}
  value=$(awk -F= -v key="field$field" '$1==key {print $2}' "$BASE/$group.txt")
  test -n "$value"
  pid=$(systemctl show app-@Setting.service -p MainPID --value)
  LD_LIBRARY_PATH=/usr/local/lib NCC_NATIVE_OPERATIONS=1 "$BASE/helper" --set "$pid" "$id" "$value" "$value" > "$BASE/$id-same-value.txt" 2> "$BASE/$id-same-value-error.txt"
  grep -q '^cleanup=matched; own-receiver-removed$' "$BASE/$id-same-value.txt"
  printf '%s: same-value owner request and readback verified\n' "$id"
 done
fi
test "$FAILED" = 0

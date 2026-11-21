#!/bin/sh
# Install the updater on an already verified owner installation of this bundle.
set -eu
umask 077
BASE=/overlay/owner-apps/np1-control
UNITS=/etc/systemd/system
STAGE=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
UPDATE=$BASE/updates
test "$(id -u)" = 0
test -f "$BASE/config/config.json" && test ! -L "$BASE"
test -f "$UNITS/np1-control.service"
cmp "$UNITS/np1-control.service" "/overlay$UNITS/np1-control.service"
cd "$STAGE"
sha256sum -c SHA256SUMS >/dev/null
cmp bin/ncc "$BASE/bin/ncc" || { echo 'Deploy and verify this NCC build before installing the updater' >&2; exit 1; }
chmod 0755 bin/ncc
IFS= read -r key
IFS= read -r repository
printf '%s\n' "$repository" | grep -Eq '^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$'
test ! -L "$UPDATE"
mkdir -m 0700 -p "$UPDATE"
exec 5>"$BASE/recovery/ncc-update.lock"
flock -n 5 || exit 75
test ! -e "$UPDATE/transaction.json" || { echo 'An interrupted update must be recovered first' >&2; exit 1; }
INPUT=$(mktemp "$UPDATE/.enrollment.XXXXXX")
trap 'rm -f "$INPUT"' 0
BACKUP=$(mktemp -d "$BASE/recovery/updater.XXXXXX")
printf '%s\n' "$key" > "$INPUT"
bin/ncc --mode update-key-check --update-key "$INPUT"
SERVICES='np1-control-update-recover.service np1-control-update.service'
for file in public.key repository verify.sh; do
    if test -f "$UPDATE/$file"; then cp -p "$UPDATE/$file" "$BACKUP/$file"; fi
done
if test -f "$BASE/bin/ncc-updater"; then cp -p "$BASE/bin/ncc-updater" "$BACKUP/ncc-updater"; fi
for unit in $SERVICES; do
    if test -f "$UNITS/$unit"; then
        cp -p "$UNITS/$unit" "$BACKUP/$unit"
        if systemctl is-enabled --quiet "$unit"; then touch "$BACKUP/$unit.enabled"; fi
        if systemctl is-active --quiet "$unit"; then touch "$BACKUP/$unit.active"; fi
    fi
done
finish() {
    result=$?
    trap - 0
    rm -f "$INPUT"
    if test "$result" -ne 0; then
        set +e
        systemctl disable --now np1-control-update.service np1-control-update-recover.service
        for file in public.key repository verify.sh; do
            if test -f "$BACKUP/$file"; then cp -p "$BACKUP/$file" "$UPDATE/$file"; else rm -f "$UPDATE/$file"; fi
        done
        if test -f "$BACKUP/ncc-updater"; then cp -p "$BACKUP/ncc-updater" "$BASE/bin/ncc-updater"; else rm -f "$BASE/bin/ncc-updater"; fi
        for unit in $SERVICES; do
            if test -f "$BACKUP/$unit"; then cp -p "$BACKUP/$unit" "$UNITS/$unit"; else rm -f "$UNITS/$unit"; fi
        done
        systemctl daemon-reload
        for unit in $SERVICES; do
            if test -f "$BACKUP/$unit.enabled"; then systemctl enable "$unit"; fi
            if test -f "$BACKUP/$unit.active"; then systemctl start "$unit"; fi
        done
        echo 'Updater enrollment failed. Previous updater files restored where available.' >&2
    fi
    exit "$result"
}
trap finish 0
# A source/key change requires an explicit operator migration of the existing enrollment.
if test -f "$UPDATE/public.key"; then cmp "$INPUT" "$UPDATE/public.key"; fi
if test -f "$UPDATE/repository"; then test "$(cat "$UPDATE/repository")" = "$repository"; fi
systemctl stop np1-control-update.service 2>/dev/null || true
cp "$INPUT" "$UPDATE/public.key"
printf '%s\n' "$repository" > "$UPDATE/repository"
cp tools/verify.sh "$UPDATE/verify.sh"
chmod 0600 "$UPDATE/verify.sh"
cp "$BASE/bin/ncc" "$BASE/bin/ncc-updater.new"
chmod 0755 "$BASE/bin/ncc-updater.new"
mv -f "$BASE/bin/ncc-updater.new" "$BASE/bin/ncc-updater"
for unit in $SERVICES; do
    cp "systemd/$unit" "$UNITS/$unit"
    chmod 0644 "$UNITS/$unit"
    cmp "systemd/$unit" "/overlay$UNITS/$unit"
done
systemctl daemon-reload
systemctl enable $SERVICES
systemctl start np1-control-update.service
for attempt in 1 2 3 4 5; do
    if test -S /run/np1-control-update/control.sock; then break; fi
    sleep 1
done
test -S /run/np1-control-update/control.sock
cmp "$BASE/bin/ncc" "$BASE/bin/ncc-updater"
sync
echo 'NP1CC updater installed. Public key, units, and control socket verified.'

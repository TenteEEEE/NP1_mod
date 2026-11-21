#!/bin/sh
# Keep the owner's update settings, key and binary backups for recovery.
set -eu
BASE=/overlay/owner-apps/np1-control
UNITS=/etc/systemd/system
if test -f "$UNITS/np1-control-update.service"; then
    systemctl disable --now np1-control-update.service np1-control-update-recover.service
    rm -f "$UNITS/np1-control-update.service" "$UNITS/np1-control-update-recover.service" "$BASE/bin/ncc-updater"
    systemctl daemon-reload
fi

# Source before replacing bin/ncc in an owner deployment or recovery script.
if test -f "$BASE/bin/ncc-updater" && test "${NCC_UPDATE_LOCK_HELD:-}" != 1; then
    exec 5>"$BASE/recovery/ncc-update.lock"
    flock -n 5 || { echo 'Another NP1CC deployment or update is active' >&2; exit 75; }
    test ! -e "$BASE/updates/transaction.json" || { echo 'An interrupted NP1CC update must be recovered first' >&2; exit 75; }
    NCC_UPDATE_LOCK_HELD=1
    export NCC_UPDATE_LOCK_HELD
fi

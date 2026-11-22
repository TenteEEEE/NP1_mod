#!/bin/sh
test "${interface:-}" = wlan0 || exit 1
if test "${1:-}" = bound; then
    printf 'NCC_LEASE=%s|%s|%s|%s\n' "${ip:-}" "${subnet:-}" "${router:-}" "${lease:-}"
fi

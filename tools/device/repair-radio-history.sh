#!/bin/sh
# Operates only on NCC-owned history. Never changes stock services or networking.
# plan is read-only. apply/restore require a separate explicit device-write window.
set -eu
base=/overlay/owner-apps/np1-control
# /overlay is mounted noexec on the device. The broker stages the same verified
# binary in /run, where owner programs are executed.
binary=/run/np1-control/ncc
config=$base/config/config.json
state=$("$binary" --mode radio-history-repair --config "$config" --radio-repair-state-dir)
[ "$state" = "$base/state" ] || { echo 'Unexpected NCC history directory' >&2; exit 2; }
action=${1:-plan}
track=${2:-}
repair(){
  if [ -n "$track" ]; then
    "$binary" --mode radio-history-repair --config "$config" --radio-repair-track "$track" "$@"
  else
    "$binary" --mode radio-history-repair --config "$config" "$@"
  fi
}

case "$action" in
  plan)
    repair
    exit
    ;;
  apply|restore) ;;
  *) echo 'usage: repair-radio-history.sh plan|apply|restore BACKUP_DIRECTORY' >&2; exit 2 ;;
esac

was_active=no
if systemctl is-active --quiet np1-control.service; then was_active=yes; fi
resume() {
  if [ "$was_active" = yes ]; then systemctl start np1-control.service; fi
}
trap resume EXIT
trap 'exit 1' HUP INT TERM
systemctl stop np1-control.service
if systemctl is-active --quiet np1-control.service; then echo 'NCC logger is still active' >&2; exit 1; fi

if [ "$action" = apply ]; then
  backup=$base/private/radio-repair-$(date -u +%Y%m%dT%H%M%SZ)-$$
  umask 077
  mkdir -p "$backup"
  : > "$backup/manifest.sha256"
  : > "$backup/owners"
  for file in radio-observations.jsonl radio-observations.jsonl.1; do
    [ -f "$state/$file" ] || continue
    [ ! -L "$state/$file" ] || { echo 'Symlink history refused' >&2; exit 1; }
    cp -p "$state/$file" "$backup/$file"
    (cd "$backup" && sha256sum "$file") >> "$backup/manifest.sha256"
    printf '%s %s\n' "$file" "$(stat -c '%u:%g' "$state/$file")" >> "$backup/owners"
  done
  echo "Backup: $backup"
  if [ -n "$track" ]; then
    cp -p "$track" "$backup/imported-track.json"
    sha256sum "$backup/imported-track.json" > "$backup/imported-track.sha256"
    track=$backup/imported-track.json
  fi
  repair > "$backup/plan.json"
  if ! repair --radio-repair-apply > "$backup/result.json"; then
    echo "Repair failed; restore from $backup before resuming normal use" >&2
    while read -r file owner; do cp -p "$backup/$file" "$state/$file"; done < "$backup/owners"
    exit 1
  fi
  while read -r file owner; do chown "$owner" "$state/$file"; done < "$backup/owners"
  if ! repair --radio-repair-check > "$backup/readback.json"; then
    while read -r file owner; do cp -p "$backup/$file" "$state/$file"; done < "$backup/owners"
    echo 'Read-back failed; restored the original history' >&2
    exit 1
  fi
  cat "$backup/result.json" "$backup/readback.json"
else
  backup=${2:?Backup directory required}
  [ "$(dirname "$backup")" = "$base/private" ] || { echo 'Invalid backup directory' >&2; exit 2; }
  case "$backup" in "$base"/private/radio-repair-*) ;; *) echo 'Invalid backup directory' >&2; exit 2 ;; esac
  case "$backup" in *..*) echo 'Invalid backup path' >&2; exit 2 ;; esac
  [ -d "$backup" ] && [ ! -L "$backup" ] || exit 2
  (cd "$backup" && sha256sum -c manifest.sha256)
  # Filenames are fixed; never evaluate names or shell text from the manifest.
  for file in radio-observations.jsonl radio-observations.jsonl.1; do
    [ -f "$backup/$file" ] || continue
    cp -p "$backup/$file" "$state/$file"
    [ "$(sha256sum "$backup/$file" | cut -d ' ' -f 1)" = "$(sha256sum "$state/$file" | cut -d ' ' -f 1)" ] || exit 1
  done
  echo 'NCC radio history restored and verified'
fi

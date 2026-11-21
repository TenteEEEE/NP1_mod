"""Persist NCC's recorded-track matches in one day's PICS history over ADB."""

import argparse
from datetime import date
import hashlib
import json
from pathlib import Path
import re
import shlex
import subprocess
import tempfile

BASE = "/overlay/owner-apps/np1-control"
SERVICE = "np1-control.service"
PATCH = """#!/bin/sh
set -eu
umask 077
expected=$1 original=$2 replacement=$3 file=$4 base=$5 unit=$6 backup=$7
digest() { sha256sum "$1" | cut -d ' ' -f 1; }
[ -f "$file" ] && [ ! -L "$file" ] && [ "$(digest "$file")" = "$original" ]
[ -f "$replacement" ] && [ "$(digest "$replacement")" = "$expected" ]
systemctl is-active --quiet "$unit"
temp=
installed=no
finish() {
  result=$?
  trap - EXIT
  if [ "$result" != 0 ] && [ "$installed" = yes ]; then
    cp -p "$backup/original.jsonl" "$temp" && mv -f "$temp" "$file" || result=1
  fi
  if [ -n "$temp" ]; then rm -f "$temp"; fi
  systemctl start "$unit" || result=1
  exit "$result"
}
trap finish EXIT
trap 'exit 1' HUP INT TERM
systemctl stop "$unit"
[ "$(systemctl show -p ActiveState --value "$unit")" = inactive ]
[ "$(digest "$file")" = "$original" ]
mkdir -p "$base/private"
mkdir "$backup"
cp -p "$file" "$backup/original.jsonl"
[ "$(digest "$backup/original.jsonl")" = "$original" ]
temp=$(mktemp "$file.backfill.XXXXXX")
cp -p "$file" "$temp"
cat "$replacement" > "$temp"
[ "$(digest "$temp")" = "$expected" ]
mv -f "$temp" "$file"
installed=yes
[ "$(digest "$file")" = "$expected" ]
echo BACKFILL_VERIFIED
"""


def candidate(raw, history):
    """Keep original rows and order, adding only matches returned by the history API."""
    lines = raw.splitlines(keepends=True)
    entries = iter(history)
    restored = 0
    for i, line in enumerate(lines):
        if not line.strip():
            continue
        original = json.loads(line)
        matched = next(entries, None)
        preserved = {
            key: value for key, value in original.items()
            if key not in ("position", "course") or value is not None
        }
        if not matched or any(matched.get(key) != value for key, value in preserved.items()):
            raise ValueError("The saved history and API snapshot differ")
        if original.get("position") is not None or not matched.get("position"):
            continue
        coordinates = matched["position"]
        if not (-90 <= coordinates["lat"] <= 90 and -180 <= coordinates["lon"] <= 180):
            raise ValueError("Invalid matched coordinates")
        original["position"] = coordinates
        if original.get("course") is None and matched.get("course") is not None:
            original["course"] = matched["course"]
        lines[i] = (json.dumps(original, ensure_ascii=False, separators=(",", ":")) + "\n").encode()
        restored += 1
    if next(entries, None) is not None:
        raise ValueError("The API snapshot contains additional history rows")
    return b"".join(lines), restored


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--serial", required=True)
    parser.add_argument("--date", required=True, type=date.fromisoformat)
    parser.add_argument("--evidence", required=True, type=Path)
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    day = args.date.isoformat()
    evidence = args.evidence.resolve()
    repo = Path(__file__).resolve().parents[2]
    if repo / "tmp" not in evidence.parents or evidence.exists():
        parser.error("Choose a new evidence directory under tmp")
    evidence.mkdir(parents=True)
    adb = ["adb", "-s", args.serial]

    def run(*command):
        result = subprocess.run(adb + list(command), capture_output=True, timeout=90)
        if result.returncode:
            (evidence / "adb-error").write_bytes(result.stdout + result.stderr)
            raise RuntimeError("NP1 operation failed; inspect ignored evidence")
        return result.stdout

    path = f"{BASE}/state/pics/receptions/{day}.jsonl"
    original = evidence / "original.jsonl"
    run("pull", path, str(original))
    snapshot = run("shell", f"wget -qO- 'http://127.0.0.1/api/v1/pics/history?date={day}'")
    (evidence / "api-before.json").write_bytes(snapshot)
    history = json.loads(snapshot)
    if history.get("date") != day or history.get("incomplete"):
        raise RuntimeError("A complete history snapshot is required")
    raw = original.read_bytes()
    replacement, restored = candidate(raw, history["entries"])
    print(f"History rows: {len(history['entries'])}; recoverable locations: {restored}")
    if not args.apply or not restored:
        return

    digest = lambda data: hashlib.sha256(data).hexdigest()
    stage = run("shell", "mktemp -d /tmp/ncc-pics-backfill.XXXXXX").decode().strip()
    if not re.fullmatch(r"/tmp/ncc-pics-backfill\.[A-Za-z0-9]+", stage):
        raise RuntimeError("Unexpected device staging path")
    backup = f"{BASE}/private/pics-backfill-{day}-{stage.rsplit('.', 1)[-1]}"
    manifest = {"date": day, "backup": backup, "before": digest(raw), "after": digest(replacement), "restored": restored}
    (evidence / "manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf8")
    try:
        with tempfile.TemporaryDirectory(dir=evidence) as directory:
            candidate_file, script = Path(directory) / "candidate", Path(directory) / "apply.sh"
            candidate_file.write_bytes(replacement)
            script.write_bytes(PATCH.encode())
            run("push", str(candidate_file), stage + "/candidate")
            run("push", str(script), stage + "/apply.sh")
            operation = ["sh", stage + "/apply.sh", manifest["after"], manifest["before"], stage + "/candidate", path, BASE, SERVICE, backup]
            if b"BACKFILL_VERIFIED" not in run("shell", shlex.join(operation)):
                raise RuntimeError("Device did not confirm the backfill")
        observed = evidence / "readback.jsonl"
        run("pull", path, str(observed))
        if observed.read_bytes() != replacement:
            raise RuntimeError("PICS history read-back differs from the replacement")
        print(f"Persisted and verified {restored} locations on NP1")
    finally:
        run("shell", f"rm -f {stage}/candidate {stage}/apply.sh; rmdir {stage}")


if __name__ == "__main__":
    main()

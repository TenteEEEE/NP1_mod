"""Fill in positions of plate records that were stored without one.

Until the sensor followed the stock per-second position, most records got no position: the
recorder writes its NMEA log only when a clip closes, and the device's own backfill can only
use sidecars still on the SD card. This tool gives such past records a position from what is
left, in this order:

  logged      a logged GNSS fix within 3 s of the first or last sighting (tracks saved on the PC,
              e.g. a plate-1113 style manifest.json with per-clip "gps" or a /recordings/track
              response) -> a measured position
  estimated   time-proportional interpolation between the nearest known positions before and
              after (logged tracks, records that have a position, located radio observations).
              Used only when those positions are close in time; the record is marked
              "estimated" with the expected error from leave-one-out validation on the same data:
                stationary (both within 50 m, <= 30 min apart)  50 m
                <= 2 min apart                                    100 m
                <= 6 min apart                                    400 m
                <= 10 min apart                                   1000 m
  none        nothing near enough: the record stays without a position (never guessed)

Reads the device API (records, located radio observations, journey runs) and writes the updated
records as JSON lines per JST day; --apply appends them on the device over root ADB after
backing up the day files (the newest line for an ID wins, so nothing is rewritten in place).

    python tools/plate/fill_locations.py --device http://HOST --out tmp/plate-location/fill \
        --track tmp/plate-1113/manifest.json [--apply SERIAL]
Output contains plates and positions: keep it in tmp/ (never in Git).
"""
from __future__ import annotations

import argparse
import bisect
import collections
import json
import math
import os
import subprocess
import sys
import urllib.request
from datetime import datetime, timedelta, timezone
from pathlib import Path

JST = timezone(timedelta(hours=9))
DEVICE_PLATE = "/overlay/owner-apps/np1-control/state/plate"
LOGGED_TOLERANCE = 3.0
MAX_SPEED = 40.0  # m/s; a pair of positions implying more is not used
TIERS = [  # (max seconds between the known positions, expected error m, name)
    (120, 100, "estimated<=2min"),
    (360, 400, "estimated<=6min"),
    (600, 1000, "estimated<=10min"),
]
STATIONARY_M, STATIONARY_S, STATIONARY_ERR = 50.0, 1800, 50


def ts(s: str) -> float:
    return datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()


def iso(t: float) -> str:
    return datetime.fromtimestamp(t, timezone.utc).isoformat().replace("+00:00", "Z")


def metres(a, b) -> float:
    r = 6371008.8
    la1, la2 = math.radians(a[1]), math.radians(b[1])
    h = math.sin((la2 - la1) / 2) ** 2 + math.cos(la1) * math.cos(la2) * math.sin(math.radians(b[2] - a[2]) / 2) ** 2
    return 2 * r * math.asin(math.sqrt(min(1.0, h)))


def fetch(base: str, path: str):
    req = urllib.request.Request(base.rstrip("/") + path, headers={"Accept-Encoding": "identity"})
    with urllib.request.urlopen(req, timeout=120) as r:
        return json.loads(r.read().decode("utf-8"))


def load_track(path: Path) -> list[tuple[float, float, float]]:
    """A plate-1113 style manifest (clips with start and [offset, lat, lon]) or a track response."""
    v = json.loads(path.read_text(encoding="utf-8"))
    out = []
    if "clips" in v:
        for c in v["clips"]:
            start = ts(c["start"])
            out += [(start + off, lat, lon) for off, lat, lon in c.get("gps", [])]
    else:
        d = v.get("data", v)
        out += [(ts(p["at"]), p["lat"], p["lon"]) for p in d.get("points", [])]
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--device", required=True, help="device web UI base URL")
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--days", type=int, default=30)
    ap.add_argument("--track", type=Path, nargs="*", default=[], help="saved GNSS tracks (logged fixes)")
    ap.add_argument("--apply", metavar="SERIAL", help="append the filled records on the device over root ADB")
    a = ap.parse_args()
    a.out.mkdir(parents=True, exist_ok=True)
    today = datetime.now(JST).date()
    frm = (today - timedelta(days=a.days - 1)).isoformat()
    records = [x.get("record", x) for x in fetch(a.device, f"/api/v1/plate/records?from={frm}&to={today.isoformat()}&limit=5000")["records"]]
    radio = fetch(a.device, f"/api/v1/radio/history?days={min(a.days, 365)}&includeUnknownAccuracy=true")
    radio = radio.get("data", radio).get("points", [])
    trips = fetch(a.device, f"/api/v1/trips?period={a.days if a.days in (7, 30, 90, 365) else 30}")
    runs = [(ts(r["start"]), ts(r["end"])) for r in trips.get("data", trips).get("trips", [])]
    (a.out / "inputs.json").write_text(json.dumps({"records": records, "radio": radio, "runs": runs}, ensure_ascii=False), encoding="utf-8")

    logged = sorted(p for path in a.track for p in load_track(path))
    logged_t = [p[0] for p in logged]
    known = list(logged)
    known += [(ts(r["location"]["at"]), r["location"]["lat"], r["location"]["lon"]) for r in records if r.get("location") and not r["location"].get("estimated")]
    known += [(ts(p["gps"]["at"]), p["gps"]["lat"], p["gps"]["lon"]) for p in radio if p.get("gps")]
    known.sort()
    known_t = [p[0] for p in known]

    def nearest_logged(t):
        i = bisect.bisect_left(logged_t, t)
        c = [logged[k] for k in (i - 1, i) if 0 <= k < len(logged) and abs(logged[k][0] - t) <= LOGGED_TOLERANCE]
        return min(c, key=lambda p: abs(p[0] - t)) if c else None

    def locate(r):
        first, last = ts(r["firstSeen"]), ts(r["lastSeen"])
        p = nearest_logged(first) or nearest_logged(last)
        if p:
            return "logged", {"at": iso(p[0]), "lat": p[1], "lon": p[2]}
        i = bisect.bisect_left(known_t, first)
        if 0 < i < len(known):
            before, after = known[i - 1], known[i]
            span, d = after[0] - before[0], metres(known[i - 1], known[i])
            if span > 0 and d / span <= MAX_SPEED:
                f = (first - before[0]) / span
                pos = {"at": iso(first), "lat": before[1] + (after[1] - before[1]) * f, "lon": before[2] + (after[2] - before[2]) * f, "estimated": True}
                if d <= STATIONARY_M and span <= STATIONARY_S:
                    return "estimated-stationary", {**pos, "accuracyM": STATIONARY_ERR}
                for limit, err, name in TIERS:
                    if span <= limit:
                        return name, {**pos, "accuracyM": err}
        covered = any(s - 60 <= first <= e + 60 for s, e in runs)
        return ("none: fixes logged but no position kept nearby" if covered else "none: no position kept for that time"), None

    counts = collections.Counter()
    by_day = collections.defaultdict(list)
    for r in records:
        if r.get("location"):
            continue
        kind, loc = locate(r)
        counts[kind] += 1
        if loc:
            r = {**r, "location": loc}
            by_day[datetime.fromtimestamp(ts(r["firstSeen"]), JST).date().isoformat()].append(r)
    files = a.out / "records"
    files.mkdir(exist_ok=True)
    for old in files.glob("*.jsonl"):
        old.unlink()
    for day, rs in by_day.items():
        (files / f"{day}.jsonl").write_text("".join(json.dumps(r, ensure_ascii=False, separators=(",", ":")) + "\n" for r in rs), encoding="utf-8")
    summary = {"records": len(records), "withoutPosition": sum(counts.values()), "filled": sum(len(v) for v in by_day.values()), "byKind": dict(counts)}
    (a.out / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=1), encoding="utf-8")
    print(json.dumps(summary, ensure_ascii=False, indent=1))
    if a.apply and by_day:
        apply(a.apply, files, a.device)


def apply(serial: str, files: Path, device: str):
    adb = ["adb", "-s", serial]
    env = dict(os.environ, MSYS_NO_PATHCONV="1")
    if "uid=0(root)" not in subprocess.check_output(adb + ["shell", "id"], env=env).decode():
        raise SystemExit("Root ADB required")
    stage = "/run/np1-plate-fill"
    subprocess.run(adb + ["shell", f"rm -rf {stage}; mkdir -m 0700 {stage}"], check=True, env=env)
    for f in sorted(files.glob("*.jsonl")):
        subprocess.run(adb + ["push", str(f), f"{stage}/{f.name}"], check=True, env=env, stdout=subprocess.DEVNULL)
    script = f"""set -e
P={DEVICE_PLATE}
B=/overlay/owner-apps/np1-control/recovery/plate-fill-$(date -u +%Y%m%dT%H%M%SZ)
mkdir -m 0700 -p "$B"
for f in {stage}/*.jsonl; do n=$(basename "$f"); test -f "$P/records/$n" || continue; cp -p "$P/records/$n" "$B/$n"; cat "$f" >> "$P/records/$n"; done
rm -rf {stage}
echo "filled; backup $B"
"""
    out = subprocess.run(adb + ["shell", script], check=True, env=env, capture_output=True, text=True)
    print(out.stdout.strip())
    after = [x.get("record", x) for x in fetch(device, "/api/v1/plate/records?limit=5000&from=" + min(f.stem for f in files.glob("*.jsonl")))["records"]]
    print("device now lists", sum(1 for r in after if r.get("location")), "of", len(after), "records with a position")


if __name__ == "__main__":
    sys.exit(main())

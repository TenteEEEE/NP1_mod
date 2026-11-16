#!/bin/sh
# Bounded, read-only forensic reads of the mounted recording SD partition.
# No path argument, no output file, no mount change and no raw-block writes.
set -eu
test "$(id -u)" = 0
device=$(awk '$2 == "/mnt/sdcard" && $3 == "vfat" { print $1 }' /proc/mounts)
case "$device" in /dev/mmcblk[0-9]p[0-9]) ;; *) echo 'Recording SD partition unavailable' >&2; exit 2 ;; esac
test -b "$device"
sectors=$(cat "/sys/class/block/${device##*/}/size")
read_range(){
  offset=$1
  count=$2
  case "$offset:$count" in *[!0-9:]*|:*|*:) exit 2 ;; esac
  test "$count" -gt 0 && test "$count" -le 4096
  test "$offset" -lt "$sectors"
  test "$((offset + count))" -le "$sectors"
  if test "$batch" = yes; then printf '%s %s\n' "$offset" "$count"; fi
  dd if="$device" bs="$((count * 512))" skip="$((offset * 512))" count=1 iflag=skip_bytes,fullblock 2>/dev/null
}
batch=no
if test "${1:-}" = --batch || test "${1:-}" = --nmea; then
  # Reuse one read-only file descriptor instead of spawning dd for thousands
  # of small free-cluster ranges. Only built-in modules are needed on NP1.
  python3 -c '
import os, sys
device, sectors = sys.argv[1], int(sys.argv[2])
mode = sys.argv[3]
dates = sys.argv[4].split(",") if len(sys.argv) > 4 else []
if mode == "--nmea" and (not dates or not all(len(v) == 6 and v.isdigit() for v in dates)):
    raise ValueError("Explicit NMEA UTC dates required")
fd = os.open(device, os.O_RDONLY)
total = 0
try:
    for index, line in enumerate(sys.stdin):
        values = line.split()
        if len(values) != 2 or not all(v.isdigit() for v in values):
            raise ValueError("Invalid sector range")
        offset, count = map(int, values)
        total += count
        if index >= 2048 or count <= 0 or count > 4096 or total > 262144 or offset < 0 or offset + count > sectors:
            raise ValueError("Sector range exceeds read-only bounds")
        os.lseek(fd, offset * 512, os.SEEK_SET)
        data = bytearray()
        while len(data) < count * 512:
            part = os.read(fd, count * 512 - len(data))
            if not part:
                raise IOError("Short SD read")
            data.extend(part)
        if mode == "--nmea":
            sentences = []
            matched = False
            cursor = 0
            while True:
                start = data.find(b"$G", cursor)
                if start < 0:
                    break
                cursor = start + 2
                if data[start:start+7] not in (b"$GPRMC,", b"$GNRMC,", b"$GPGGA,", b"$GNGGA,"):
                    continue
                star = data.find(b"*", start, start + 256)
                if star < 0 or star + 3 > len(data):
                    continue
                line = bytes(data[start:star+3])
                sentences.append(line)
                fields = line[1:-3].split(b",")
                actual = 0
                for value in line[1:-3]:
                    actual ^= value
                try:
                    valid = actual == int(line[-2:], 16)
                except ValueError:
                    valid = False
                if valid and fields[0].endswith(b"RMC") and len(fields) > 9 and fields[2] == b"A" and fields[9].decode("ascii", "replace") in dates:
                    matched = True
            payload = b"\n".join(sentences) + b"\n" if matched else b""
            sys.stdout.buffer.write(("%d %d %d\n" % (offset, count, len(payload))).encode("ascii"))
            sys.stdout.buffer.write(payload)
        else:
            sys.stdout.buffer.write(("%d %d\n" % (offset, count)).encode("ascii"))
            sys.stdout.buffer.write(data)
    sys.stdout.buffer.flush()
finally:
    os.close(fd)
' "$device" "$sectors" "$1" "${2:-}"
else
  read_range "${1:?sector offset required}" "${2:?sector count required}"
fi

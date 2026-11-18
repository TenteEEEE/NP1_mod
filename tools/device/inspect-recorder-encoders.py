#!/usr/bin/env python3
"""Read-only inventory of pinned PMF_GStreamerVideoEncoder objects in DriveRec."""
import os
import struct
import sys

pid = int(sys.argv[1]) if len(sys.argv) == 2 else 0
if pid <= 1 or os.readlink("/proc/{}/exe".format(pid)) != "/usr/local/bin/DriveRec":
    raise SystemExit("expected DriveRec pid")

maps = open("/proc/{}/maps".format(pid)).read().splitlines()
mps = 0
for line in maps:
    fields = line.split()
    if len(fields) >= 6 and fields[2] == "00000000" and fields[5].endswith("/libNPMultimediaPlayerStack.so"):
        mps = int(fields[0].split("-")[0], 16)
if not mps:
    raise SystemExit("MPS mapping missing")

needle = struct.pack("<Q", mps + 0x19B918 + 16)
with open("/proc/{}/mem".format(pid), "rb", buffering=0) as memory:
    for line in maps:
        fields = line.split()
        start_text, end_text = fields[0].split("-")
        start, end = int(start_text, 16), int(end_text, 16)
        if len(fields) < 2 or not fields[1].startswith("rw"):
            continue
        size = end - start
        try:
            memory.seek(start)
            data = memory.read(size)
        except OSError:
            continue
        at = 0
        while True:
            at = data.find(needle, at)
            if at < 0:
                break
            if at + 0x2D0 <= len(data):
                values = struct.unpack_from("<dIIIIII", data, at + 0x278)
                pipeline = struct.unpack_from("<Q", data, at + 0x2C8)[0]
                output = struct.unpack_from("<Q", data, at + 0x2A0)[0]
                print("object=0x{:x} map={} fps={:.3f} width={} height={} codec={} padding={} rate={} bitrate={} gop={} output=0x{:x} pipeline=0x{:x}".format(start+at, size, values[0], values[1], values[2], values[3], values[4], values[5], values[6], struct.unpack_from("<I", data, at+0x298)[0], output, pipeline))
            at += 8

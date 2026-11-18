#!/usr/bin/env python3
"""Read-only DriveRec runtime-hook diagnostics; emits mapping-relative values."""
import argparse
import struct


parser = argparse.ArgumentParser()
parser.add_argument("--pid", type=int, required=True)
parser.add_argument("--status-offset", type=lambda value: int(value, 0), required=True)
args = parser.parse_args()
if args.pid <= 1:
    raise SystemExit("invalid pid")

hook_path = "/run/np1-control-recorder-hook/libncc-driverec-hook.so"
maps = []
with open("/proc/%d/maps" % args.pid) as stream:
    for line in stream:
        fields = line.split()
        if len(fields) < 6:
            continue
        start, end = (int(value, 16) for value in fields[0].split("-"))
        path = fields[5]
        if path.endswith(" (deleted)"):
            path = path[:-10]
        maps.append((start, end, fields[1], int(fields[2], 16), path))


def base_for(suffix):
    values = [start for start, _, _, offset, path in maps if offset == 0 and path.endswith(suffix)]
    if len(values) != 1:
        raise RuntimeError("mapping unavailable: %s" % suffix)
    return values[0]


image = base_for("/usr/local/bin/DriveRec")
multimedia = base_for("/usr/local/lib/libNPMultimediaPlayerStack.so")
hook = base_for(hook_path)
hook_exec = [(start, end) for start, end, permissions, _, path in maps if path == hook_path and "x" in permissions]
if len(hook_exec) != 1:
    raise RuntimeError("hook executable mapping unavailable")

with open("/proc/%d/mem" % args.pid, "rb", buffering=0) as memory:
    def read(address, size):
        memory.seek(address)
        data = memory.read(size)
        if len(data) != size:
            raise RuntimeError("short process-memory read")
        return data

    got = struct.unpack("<Q", read(image + 0x22CDB0, 8))[0]
    stream_write = struct.unpack("<Q", read(multimedia + 0x1970A0, 8))[0]
    status = struct.unpack("<Q10I", read(hook + args.status_offset, 48))

start, end = hook_exec[0]
print("hook_exec_size=0x%x" % (end - start))
print("streamer_got_hook_relative=%s" % (hex(got - hook) if hook <= got < end else "outside"))
print("stream_write_hook_relative=%s" % (hex(stream_write - hook) if hook <= stream_write < end else "outside"))
print("status=" + ",".join(hex(value) for value in status))

#!/usr/bin/env python3
"""Measure the DriveRec encoder tap without opening an SD recording."""

import argparse
import os
import socket
import struct
import time


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", choices=("front", "in"), required=True)
    parser.add_argument("--seconds", type=float, default=5.0)
    args = parser.parse_args()
    source_id = 1 if args.source == "front" else 2
    path = "/run/np1-control/camera-encoder-{}.sock".format(args.source)
    try:
        os.unlink(path)
    except FileNotFoundError:
        pass
    channel = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)
    channel.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, 4 << 20)
    actual_buffer = channel.getsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF)
    channel.bind(path)
    os.chmod(path, 0o666)
    channel.settimeout(1)
    started = time.monotonic()
    frames = chunks = malformed = 0
    first_pts = last_pts = None
    sequences = set()
    try:
        while time.monotonic() - started < args.seconds:
            try:
                packet = channel.recv(65536)
            except TimeoutError:
                continue
            chunks += 1
            if len(packet) < 56 or packet[:8] != b"NCCAU001":
                malformed += 1
                continue
            source, header, sequence, total, offset, amount, width, height, fps_milli, nal_type, pts = struct.unpack_from("<10IQ", packet, 8)
            if source != source_id or header != 56 or len(packet) != header + amount:
                malformed += 1
                continue
            if sequence and sequence not in sequences:
                sequences.add(sequence)
                frames += 1
                first_pts = pts if first_pts is None else first_pts
                last_pts = pts
    finally:
        channel.close()
        try:
            os.unlink(path)
        except FileNotFoundError:
            pass
    elapsed = time.monotonic() - started
    span = None if first_pts is None or last_pts is None else last_pts - first_pts
    print({"source": args.source, "seconds": elapsed, "frames": frames, "chunks": chunks, "fps": frames / elapsed, "firstPTS": first_pts, "lastPTS": last_pts, "ptsSpan": span, "malformed": malformed, "receiveBuffer": actual_buffer})


if __name__ == "__main__":
    main()

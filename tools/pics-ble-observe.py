#!/usr/bin/env python3
"""Read the NP1's existing passive discovery traffic into a private JSONL file."""

import argparse
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import time


def manufacturer(frame, company):
    if len(frame) < 0x298:
        return None
    offset = 0x298
    length = frame[0x1EC]
    if frame[0x1FC] > 8:
        return None
    fields = [(0xFF, length)]
    for i in range(frame[0x1FC]):
        fields.append(tuple(frame[0x200 + 16*i:0x202 + 16*i]))
    for kind, length in fields:
        if length > 31 or offset + length > len(frame):
            return None
        value = frame[offset:offset+length]
        if kind == 0xFF and len(value) >= 2 and int.from_bytes(value[:2], 'little') == company:
            return value[2:]
        offset += length
    return None


def classify(payload):
    if len(payload) < 10 or len(payload) > 27:
        return 'invalid_length'
    kind = payload[2]
    if kind not in (0, 1, 2):
        return 'unknown_type'
    if len(payload) < (11, 18, 11)[kind]:
        return 'short_type_' + str(kind)
    if kind == 2 and any((value & 15) > 4 for value in payload[10:16]):
        return 'unknown_lamp_color'
    if kind == 1:
        lat = int.from_bytes(payload[10:14], 'big', signed=True) / 1e6
        lon = int.from_bytes(payload[14:18], 'big', signed=True) / 1e6
        if not (20 <= lat <= 47 and 122 <= lon <= 155):
            return 'coordinates_out_of_extent'
    return None


def capture(adb, seconds, output, tag):
    def shell(command):
        return subprocess.check_output(adb + ['shell', command], timeout=5).replace(b'\r', b'')

    address = re.search(rb"(?m)^DBUS_SESSION_BUS_ADDRESS='(unix:(?:abstract=|path=/tmp/)[^';\s]+)'", shell('cat /tmp/dbus_session_bus_address'))
    pid = shell('systemctl show -p MainPID --value np1-control-broker.service').strip()
    if not address or not pid.isdigit() or int(pid) <= 1:
        raise RuntimeError('Active stock bus and NP1 broker are required')
    rules = ["type='method_call',path='/SPF/EventBase/NPBTLEHandler',member='CallFunction'",
             "type='signal',path='/SPF/EventBase/Evt048',member='SPFEvent'"]
    cmd = ['timeout', '-t', str(seconds), 'dbus-monitor', '--address', address[1].decode('ascii'), *rules]
    proc = subprocess.Popen(adb + ['shell', shlex.join(cmd)], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    key = os.urandom(16)
    base = time.monotonic_ns()
    bus_base = None
    company = 0xFFFF if tag else 0x01CE
    counts = Counter()
    reports = 0
    matched = 0

    def event(lines, mono):
        nonlocal bus_base, reports, matched
        header = lines[0]
        frame_dump = b''.join(lines)
        integers = re.findall(rb'uint32 (\d+)', frame_dump)
        stamp = re.search(rb'time=(\d+\.\d+)', header)
        if not stamp:
            return
        bus_time = float(stamp[1])
        if bus_base is None:
            bus_base = bus_time
        entry = {'ts_utc': datetime.fromtimestamp(bus_time, timezone.utc).isoformat(),
                 'bus_elapsed_ms': round((bus_time-bus_base)*1000, 3),
                 'mono_ms': (mono-base)//1_000_000, 'receiver_id': 'np1', 'parser_version': 'stock-1'}
        match = re.search(rb'array of bytes\s*\[([\s0-9a-fA-F]+)\]', frame_dump)
        if not match:
            return
        wire = bytes.fromhex(match[1].decode('ascii'))
        if b'member=CallFunction' in header and integers[:3] == [b'1', pid, b'15']:
            if len(wire) != 24:
                return
            entry.update(event='scan_request', duration_seconds=int.from_bytes(wire[16:20], 'little'))
        elif b'member=SPFEvent' in header and integers[:2] == [pid, b'0']:
            if wire[:12] != bytes.fromhex('c80200000000000030000000'):
                return
            frame = wire[12:]
            if len(frame) >= 0x298 and frame[0x291] in (1, 2):
                entry.update(event='scan_end', status=frame[0x291], reports=reports, matched=matched)
                reports = matched = 0
            else:
                reports += 1
                payload = manufacturer(frame, company)
                if payload is None or tag and not payload.startswith(tag):
                    return
                matched += 1
                payload = payload[len(tag):]
                reason = classify(payload)
                # The stock callback sign-extends the controller RSSI before packing it here.
                rssi = int.from_bytes(frame[0x30:0x34], 'little', signed=True)
                entry.update(event='advertisement', source=hashlib.blake2s(frame[0x20:0x26], key=key, digest_size=6).hexdigest(),
                             ad_kind='manufacturer_data', company_id=f'0x{company:04X}', rssi_dbm=rssi if -127 <= rssi <= 20 else None,
                             payload_hex=payload.hex(), valid=reason is None, error=reason)
                if len(payload) >= 11 and payload[2] == 2:
                    entry.update(message_id=payload[3], lamp_codes=[x & 15 for x in payload[10:16]])
        else:
            return
        output.write(json.dumps(entry, ensure_ascii=True) + '\n')
        output.flush()
        counts[entry['event']] += 1

    try:
        lines, mono = [], base
        for line in proc.stdout:
            if re.match(rb'^(signal |method call |method return |error )', line):
                if lines:
                    event(lines, mono)
                lines, mono = [], time.monotonic_ns()
            if len(lines) < 512:
                lines.append(line)
        if lines:
            event(lines, mono)
    finally:
        if proc.poll() is None:
            proc.terminate()
        proc.wait(timeout=5)
    if not (counts['scan_request'] or counts['scan_end']):
        raise RuntimeError('No stock discovery clock captured. Confirm the PICS receiver is enabled.')
    print(dict(counts))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--serial', required=True)
    parser.add_argument('--seconds', type=int, default=60, choices=range(5, 601), metavar='5..600')
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--bench-tag', type=bytes.fromhex, default=b'')
    args = parser.parse_args()
    if args.bench_tag and len(args.bench_tag) != 8:
        parser.error('--bench-tag requires eight bytes')
    path = args.output.resolve()
    root = Path(__file__).resolve().parents[1]
    if not any(v in path.parents for v in (root/'tmp', root/'docs_private')):
        parser.error('Store the capture under tmp or docs_private')
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'w', encoding='utf-8') as output:
        capture(['adb', '-s', args.serial], args.seconds, output, args.bench_tag)


if __name__ == '__main__':
    main()

#!/bin/sh
# One self-expiring, passive stock discovery. Capture counts only, never nearby device data.
set -eu
export SYSTEMD_COLORS=0
exec python3 - <<'PY'
import collections
import os
import pathlib
import re
import struct
import subprocess
import time

bus_file = pathlib.Path('/tmp/dbus_session_bus_address')
match = re.search(r"^DBUS_SESSION_BUS_ADDRESS='(unix:[^'\n]+)'(?:;[ \t]*export DBUS_SESSION_BUS_ADDRESS;?)?[ \t]*;?[ \t]*$", bus_file.read_text(), re.MULTILINE)
if not match:
    raise SystemExit('stock session bus address unavailable')
address = match.group(1)
handler = 'NPBTLEHandler'
service = 'SPF.EventBase.{}.Stub'.format(handler)
path = '/SPF/EventBase/{}'.format(handler)
interface = 'SPF.EventBase.v1_0'
bus = ['busctl', '--address=' + address, '--timeout=2s']
units = ['NEventWatcherCom.service', 'app-@Communication.service', 'app-@DriveRec.service', 'app-@ImageRecog.service', 'app-@Voice.service', 'qmmf-server.service']

def baseline():
    return subprocess.check_output(['systemctl', 'show', '-p', 'Id,MainPID,InvocationID,ActiveState'] + units, timeout=8)

def call(function, arguments):
    # Sync, actual caller PID, no sub-IDs, bounded request and a single status byte.
    raw = struct.pack('<' + 'I'*len(arguments), *arguments)
    process = subprocess.Popen(bus + ['call', service, path, interface, 'CallFunction', 'uuuauayu',
        '1', str(os.getpid()), str(function), '0', str(len(raw))] + list(map(str, raw)) + ['1'], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    output, _ = process.communicate(timeout=4)
    if process.returncode or not re.fullmatch(rb'ayau 1 \d+ 0\s*', output):
        raise RuntimeError('unexpected stock reply')
    return int(output.split()[2])

before = baseline()
read_result = call(4, (0, 4, 4))
print('stock_read_status={}'.format(read_result), flush=True)
event_path = '/SPF/EventBase/Evt048'
signal_filter = "type='signal',path='{}',interface='{}',member='SPFEvent'".format(event_path, interface)
monitor = subprocess.Popen(['dbus-monitor', '--address', address, signal_filter], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
try:
    time.sleep(.3)
    # LE discovery (passive, 2 seconds, stock filter disabled). No StopDiscovery
    # is necessary and no existing scan is cancelled.
    print('stock_discovery_status={}'.format(call(15, (0, 15, 15, 0, 2, 0))), flush=True)
    time.sleep(3)
finally:
    monitor.terminate()
    raw, _ = monitor.communicate(timeout=2)
print('monitor_output_lines={}'.format(len(raw.splitlines())))
counts = collections.Counter()
shapes = collections.Counter()
for event in raw.split(b'\nsignal '):
    if b'member=SPFEvent' not in event:
        continue
    fields = re.findall(rb'uint32 (\d+)', event)
    if len(fields) != 2:
        continue
    array = re.search(rb'array of bytes \[([\s0-9a-f]+)\]', event)
    if not array:
        continue
    data = bytes.fromhex(' '.join(array.group(1).decode().split()))
    if len(data) < 12:
        continue
    handler_id = struct.unpack_from('<I', data, 8)[0]
    kind = struct.unpack_from('<Q', data)[0]
    if handler_id == 0x30:
        counts[kind] += 1
        shapes[kind, len(data)-12] += 1
print('stock_events=' + ','.join('{:#x}:{}'.format(kind, count) for kind, count in sorted(counts.items())))
print('event_shapes=' + ','.join('{:#x}/{}:{}'.format(kind, size, count) for (kind, size), count in sorted(shapes.items())))
print('stock_processes_unchanged={}'.format(str(before == baseline()).lower()))
print('stock_trial_complete')
PY

"""Reproducible bounded reboot test. Pinned ADB target, serial never committed.
Run: python tools/reboot-test.py --serial SERIAL --count 1
"""
import argparse
import json
import pathlib
import re
import subprocess
import sys
import time
parser = argparse.ArgumentParser()
parser.add_argument('--serial', required=True)
parser.add_argument('--count', type=int, default=1)
args = parser.parse_args()
if not re.fullmatch(r'[A-Za-z0-9._:-]+', args.serial) or not 1 <= args.count <= 10:
    raise SystemExit('Invalid serial or count')
root = pathlib.Path(__file__).resolve().parents[1]
adb = ['adb', '-s', args.serial]
results = []
for cycle in range(args.count):
    before = subprocess.check_output(adb + ['shell', 'cat', '/proc/sys/kernel/random/boot_id']).strip()
    stage = '/tmp/ncc-reboot-stage'
    subprocess.run(adb + ['shell', 'mkdir', '-p', stage], check=True)
    for name in ('verify.sh', 'reboot-check.sh'):
        subprocess.run(adb + ['push', str(root/'tools/device'/name), stage+'/'+name], check=True)
    subprocess.run(adb + ['shell', '/bin/sh', stage+'/reboot-check.sh'], check=True, timeout=30)
    start = time.monotonic()
    time.sleep(5)
    while time.monotonic() - start < 180:
        try:
            current = subprocess.check_output(adb + ['shell', 'cat', '/proc/sys/kernel/random/boot_id'], stderr=subprocess.DEVNULL, timeout=5).strip()
            if current and current != before:
                state = subprocess.check_output(adb + ['shell', 'systemctl', 'is-active', 'np1-control.service', 'np1-control-broker.service'], timeout=10).decode()
                if state.replace('\r','').split() == ['active','active']:
                    break
        except (subprocess.SubprocessError, OSError):
            pass
        time.sleep(3)
    else:
        raise SystemExit('Reboot recovery timed out; do not issue another reboot')
    subprocess.run(['python',str(root/'tools/connect.py'),'--serial',args.serial],check=True)
    verify_deadline = time.monotonic() + 90
    while True:
        verification = subprocess.run(['python',str(root/'tools/hardware_verify.py'),'--camera'], capture_output=True)
        if verification.returncode == 0:
            sys.stdout.buffer.write(verification.stdout)
            sys.stderr.buffer.write(verification.stderr)
            break
        if time.monotonic() >= verify_deadline:
            sys.stdout.buffer.write(verification.stdout)
            sys.stderr.buffer.write(verification.stderr)
            raise SystemExit('Hardware verification did not become ready after reboot')
        time.sleep(3)
    results.append({'cycle':cycle+1,'seconds':round(time.monotonic()-start,1),'status':'PASS'})
    (root/'tmp/reboot-results.json').write_text(json.dumps(results,indent=2),encoding='utf8')
    print('Reboot cycle',cycle+1,'PASS',flush=True)

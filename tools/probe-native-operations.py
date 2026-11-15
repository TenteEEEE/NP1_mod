"""Probe fixed native requests; optional setters resend current values only.

Never formats. All device writes and invocation are in tools/device.
"""
import argparse
import hashlib
import json
import pathlib
import re
import shutil
import subprocess
import time

ROOT = pathlib.Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--serial', required=True)
    parser.add_argument('--same-value', action='store_true', help='Send fixed setters with their current values only; never format.')
    args = parser.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9._:-]+', args.serial):
        parser.error('invalid serial')
    bundle = ROOT / 'artifacts/native-operations-probe'
    files = {'bin/ncc-native-operations-helper': ROOT / 'artifacts/native-operations-helper/ncc-native-operations-helper',
             'tools/native-operations-probe.sh': ROOT / 'tools/device/native-operations-probe.sh'}
    for name, source in files.items():
        target = bundle / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)
    (bundle / 'SHA256SUMS').write_text(''.join(hashlib.sha256((bundle / name).read_bytes()).hexdigest() + '  ' + name + '\n' for name in files), encoding='ascii', newline='\n')
    evidence = ROOT / 'tmp/native-operations-probe' / time.strftime('%Y%m%d-%H%M%S')
    evidence.mkdir(parents=True)
    adb = ['adb', '-s', args.serial]
    subprocess.run(adb + ['push', '--sync', str(bundle) + '/.', '/tmp/ncc-operations-probe'], check=True, stdout=subprocess.DEVNULL)
    result = subprocess.run(adb + ['shell', '/bin/sh', '/tmp/ncc-operations-probe/tools/native-operations-probe.sh'] + (['same-value'] if args.same_value else []), stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (evidence / 'transport.private.txt').write_bytes(result.stdout)
    match = re.search(rb'Private evidence: (/run/np1-native-operations-probe\.[A-Za-z0-9]+)', result.stdout)
    if match:
        subprocess.run(adb + ['pull', match.group(1).decode('ascii'), str(evidence)], check=True, stdout=subprocess.DEVNULL)
    if result.returncode or not re.search(rb'NCC_EXIT_STATUS=0\s*$', result.stdout):
        raise SystemExit('Probe failed; private evidence: ' + str(evidence))
    print('Probe finished; stock identities and saved settings preserved')
    print(result.stdout.decode('utf-8').replace('\r', '').strip())
    print('Private evidence:', evidence)


if __name__ == '__main__':
    main()

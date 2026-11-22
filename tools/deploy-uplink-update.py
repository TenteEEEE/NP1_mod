import argparse
import gzip
import hashlib
import ipaddress
import json
import pathlib
import re
import shutil
import socket
import ssl
import subprocess
import tempfile
import time


ROOT = pathlib.Path(__file__).resolve().parents[1]
FILES = (
    'bin/ncc', 'experimental/ncc-native-wifi-sta-helper', 'systemd/np1-control-uplink.service',
    'tools/ncc-wifi-dhcp-event.sh', 'tools/update-lock.sh', 'tools/uplink-worker-update.sh', 'tools/verify.sh',
)


def main():
    parser = argparse.ArgumentParser(description='Deploy only the NP1CC Web and uplink components')
    parser.add_argument('--serial', required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--bundle', type=pathlib.Path, default=ROOT / 'artifacts/ncc')
    parser.add_argument('--tailnet-certificate-file', type=pathlib.Path, help='Trusted fingerprint from an earlier USB tools/connect.py run')
    parser.add_argument('--rollback', metavar='BACKUP_NAME')
    args = parser.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9._:-]+', args.serial) or not re.fullmatch(r'\d+\.\d+\.\d+', args.version):
        parser.error('Invalid serial or version')
    if args.rollback and not re.fullmatch(r'uplink-worker\.[A-Za-z0-9]{6}', args.rollback):
        parser.error('Invalid backup name')
    adb = ['adb', '-s', args.serial]

    def run(*command, seconds=30):
        try:
            result = subprocess.run(adb + list(command), capture_output=True, text=True, timeout=seconds)
        except subprocess.TimeoutExpired:
            raise RuntimeError('ADB ' + command[0] + ' timed out') from None
        if result.returncode:
            raise RuntimeError('ADB ' + command[0] + ' failed')
        return result.stdout.strip()

    def verify_tailnet():
        if ':' not in args.serial:
            return
        host, port = args.serial.rsplit(':', 1)
        if not args.tailnet_certificate_file or port != '5556' or ipaddress.IPv4Address(host) not in ipaddress.IPv4Network('100.64.0.0/10'):
            raise RuntimeError('TCP deployment requires the previously verified tailnet ADB target and USB certificate fingerprint')
        expected = json.loads(args.tailnet_certificate_file.read_text(encoding='utf-8'))['fingerprint']
        with socket.create_connection((host, 443), timeout=5) as plain:
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
            context.check_hostname = False
            context.verify_mode = ssl.CERT_NONE
            with context.wrap_socket(plain) as tls:
                observed = hashlib.sha256(tls.getpeercert(binary_form=True)).hexdigest()
        if expected.lower() != observed:
            raise RuntimeError('Device certificate does not match the USB reference')
        subprocess.run(['adb', 'connect', args.serial], capture_output=True, check=True, timeout=8)

    try:
        verify_tailnet()
    except (OSError, ValueError, subprocess.SubprocessError):
        raise SystemExit('Tailnet connection or USB fingerprint verification failed') from None
    if run('shell', 'id -u') != '0':
        raise RuntimeError('Root ADB unavailable')
    manifest = {}
    for row in (args.bundle / 'SHA256SUMS').read_text(encoding='utf-8').splitlines():
        digest, filename = row.split('  ', 1)
        manifest[filename] = digest

    with tempfile.TemporaryDirectory(prefix='uplink-deploy-', dir=ROOT / 'tmp') as directory:
        subset = pathlib.Path(directory)
        stage = run('shell', 'umask 077; mktemp -d /run/np1-uplink-update.XXXXXX')
        if not re.fullmatch(r'/run/np1-uplink-update\.[A-Za-z0-9]{6}', stage):
            raise RuntimeError('Invalid device staging path')
        for filename in FILES:
            source = args.bundle / filename
            digest = hashlib.sha256(source.read_bytes()).hexdigest()
            if digest != manifest.get(filename):
                raise RuntimeError('Bundle checksum mismatch: ' + filename)
            local = subset / filename
            local.parent.mkdir(parents=True, exist_ok=True)
            if filename == 'bin/ncc':
                with source.open('rb') as original, local.with_suffix('.gz').open('wb') as compressed:
                    with gzip.GzipFile(filename='', mode='wb', fileobj=compressed, compresslevel=6, mtime=0) as archive:
                        shutil.copyfileobj(original, archive)
            else:
                shutil.copyfile(source, local)
        (subset / 'SHA256SUMS').write_bytes(''.join(f'{manifest[filename]}  {filename}\n' for filename in FILES).encode('ascii'))
        run('push', str(subset) + '/.', stage, seconds=480)
        prepared = run('shell', f'/bin/gunzip -c {stage}/bin/ncc.gz >{stage}/bin/ncc && '
                       f'rm -- {stage}/bin/ncc.gz && echo NCC_UPLINK_STAGED')
        if prepared != 'NCC_UPLINK_STAGED':
            raise RuntimeError('Compressed transfer could not be unpacked on the device')

    action = 'rollback ' + args.rollback if args.rollback else 'apply ' + args.version
    unit = 'ncc-uplink-update-' + stage.split('.')[-1].lower()
    command = (f'/bin/sh {stage}/tools/uplink-worker-update.sh {action} >{stage}/deployment.log 2>&1; '
               f'result=$?; echo "$result" >{stage}/job-result; exit "$result"')
    run('shell', f"systemd-run --unit={unit} --property=RuntimeMaxSec=300 /bin/sh -c '{command}'")
    print('Device job started in ' + stage, flush=True)
    deadline = time.monotonic() + 320
    while time.monotonic() < deadline:
        try:
            result = run('shell', f'cat {stage}/job-result 2>/dev/null || true', seconds=8)
            if result in ('0', '1', '64', '75', '78', '129', '130', '143') or result.isdecimal():
                report = run('shell', f'cat {stage}/deployment.log')
                log_path = ROOT / 'tmp' / (unit + '.log')
                log_path.write_text(report + '\n', encoding='utf-8')
                print('Private report: ' + str(log_path.relative_to(ROOT)), flush=True)
                for line in report.splitlines():
                    if re.fullmatch(r'Recovery backup: /overlay/owner-apps/np1-control/recovery/uplink-worker\.[A-Za-z0-9]{6}|'
                                    r'Bound (?:cellular|Wi-Fi) DNS and HTTPS verified|NP1CC [0-9.]+ installed and verified|'
                                    r'Update failed; previous software restored|Automatic rollback could not be verified|Previous NP1CC and uplink files restored', line):
                        print(line, flush=True)
                if result != '0':
                    raise SystemExit('Device action failed; inspect the private report')
                return
        except (RuntimeError, subprocess.TimeoutExpired):
            try:
                verify_tailnet()
            except (OSError, ValueError, RuntimeError, subprocess.SubprocessError):
                pass
        time.sleep(3)
    raise SystemExit('Device job completion not confirmed; do not retry before checking the logged staging path')


if __name__ == '__main__':
    main()

import argparse
import getpass
import hashlib
import json
import os
import pathlib
import re
import secrets
import shlex
import subprocess
import time
import urllib.request

import paramiko


ROOT = pathlib.Path(__file__).resolve().parents[1]
UNIT_STAGE = ROOT / 'tmp' / 'np1-uplink-candidate'
HELPER = ROOT / 'artifacts' / 'ncc-native-wifi-sta' / 'bin' / 'ncc-native-wifi-sta-helper'


def main():
    parser = argparse.ArgumentParser(description='ADB + browser + isolated USB AP uplink regression')
    parser.add_argument('--serial', required=True)
    parser.add_argument('--analysis-host', required=True)
    parser.add_argument('--analysis-user', required=True)
    parser.add_argument('--usb-interface', required=True)
    parser.add_argument('--wrong-password-only', action='store_true')
    args = parser.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9._:-]+', args.serial) or not re.fullmatch(r'wlx[a-f0-9]{12}', args.usb_interface):
        parser.error('Invalid serial or USB interface')

    adb = ['adb', '-s', args.serial]
    ssh = paramiko.SSHClient()
    ssh.load_system_host_keys(str(pathlib.Path.home() / '.ssh' / 'known_hosts'))
    ssh_password = getpass.getpass('Analysis PC SSH password: ')
    ssh.connect(args.analysis_host, username=args.analysis_user, password=ssh_password, look_for_keys=False, allow_agent=False, timeout=8)
    fixture_in = fixture_out = sftp = fixture_dir = port = stage = None
    applied = fixture_ready = False
    ssid, wifi_password = 'ncc-field-' + secrets.token_hex(4), secrets.token_hex(10)
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def run_adb(*command):
        return subprocess.check_output(adb + list(command), stderr=subprocess.PIPE, text=True, timeout=55).strip()

    def shell(command):
        return run_adb('shell', command)

    def device_command(action):
        text = shell(f'sh {stage}/trial.sh {action}; echo trial_exit=$?')
        if not re.search(r'^trial_exit=0\s*$', text, re.M):
            raise RuntimeError('Device trial action failed: ' + text)
        print(text, flush=True)

    def remote(command):
        _, output, _ = ssh.exec_command(command, timeout=12)
        result = output.read().decode().strip()
        if output.channel.recv_exit_status():
            raise RuntimeError('Analysis-PC preflight query failed')
        return result

    def fixture(action):
        fixture_in.write(json.dumps(action) + '\n')
        fixture_in.flush()
        line = fixture_out.readline()
        if not line:
            raise RuntimeError('Fixture closed unexpectedly')
        return json.loads(line)

    def check(predicate, seconds, label):
        deadline = time.monotonic() + seconds
        path = f'http://127.0.0.1:{port}/api/v1/network/uplink'
        while time.monotonic() < deadline:
            with client.open(path, timeout=5) as response:
                status = json.load(response).get('status') or {}
            if predicate(status):
                print('PASS: ' + label, flush=True)
                return status
            time.sleep(2)
        if status:
            print('Last uplink:', {key: status.get(key) for key in ('uplink', 'wpaState', 'wifiInternet', 'staOn', 'error')}, flush=True)
        raise RuntimeError('Uplink condition not confirmed: ' + label)

    def wifi_online(status):
        return status.get('staOn') is True and status.get('uplink') == 'wifi' and status.get('wifiInternet') == 'verified'

    def cell_online(status):
        return status.get('staOn') is False and status.get('uplink') == 'cellular'

    def verify_cell():
        deadline = time.monotonic() + 40
        while time.monotonic() < deadline:
            raw = shell('/run/np1-control/ncc --mode cellular-internet-probe || true')
            for line in raw.splitlines():
                if not line.startswith('{'):
                    continue
                result = json.loads(line)
                if result.get('status') == 'passed' and all(result.get(key) is True for key in ('dnsVerified', 'tlsVerified', 'httpVerified')):
                    print('PASS: 4G DNS and certificate-verified HTTPS', flush=True)
                    return
            time.sleep(2)
        raise RuntimeError('4G bound HTTPS proof failed after route convergence')

    try:
        path = '/sys/class/net/' + args.usb_interface
        device, driver, phy = remote(' && '.join('readlink -f ' + path + suffix for suffix in ('/device', '/device/driver', '/phy80211'))).splitlines()
        protected = [entry for entry in remote('ls /sys/class/net').splitlines() if entry != args.usb_interface and re.fullmatch(r'(?:wlo\d+|wlx[a-f0-9]{12})', entry)]
        fixture_dir = remote('umask 077; mktemp -d /dev/shm/ncc-uplink-fixture.XXXXXX')
        if not re.fullmatch(r'/dev/shm/ncc-uplink-fixture\.[A-Za-z0-9]{6}', fixture_dir):
            raise RuntimeError('Unexpected RAM staging path')
        sftp = ssh.open_sftp()
        for name in ('usb-uplink-fixture.py', 'usb-wifi-ap-probe.py'):
            sftp.put(str(ROOT / 'tools' / 'remote' / name), fixture_dir + '/' + name)
        command = 'sudo -k -S -p "" python3 -B -u ' + shlex.quote(fixture_dir + '/usb-uplink-fixture.py')
        fixture_in, fixture_out, _ = ssh.exec_command(command, timeout=40)
        fixture_in.write(ssh_password + '\n')
        ssh_password = ''
        ready = fixture({'allowSpareUSBWiFiChanges': True, 'ssid': ssid, 'password': wifi_password,
                         'profile': {'interface': args.usb_interface, 'phy': pathlib.PurePosixPath(phy).name,
                                     'driver': pathlib.PurePosixPath(driver).name, 'usbPath': device,
                                     'protectedInterfaces': protected}})
        if ready.get('phase') != 'prepared':
            raise RuntimeError('Fixture not ready: ' + str(ready))
        fixture_ready = True
        print('PASS: isolated hidden WPA2 SSID and DHCP active', flush=True)
        stage = shell('umask 077; mktemp -d /run/np1-uplink-candidate.XXXXXX')
        if not re.fullmatch(r'/run/np1-uplink-candidate\.[A-Za-z0-9]{6}', stage):
            raise RuntimeError('Unexpected device staging path')
        files = {'ncc': UNIT_STAGE / 'ncc', 'helper': HELPER, 'trial.sh': ROOT / 'tools/device/uplink-field-trial.sh',
                 'dhcp-event': ROOT / 'tools/device/ncc-wifi-dhcp-event.sh'}
        manifest = ''.join(hashlib.sha256(path.read_bytes()).hexdigest() + '  ' + name + '\n' for name, path in files.items())
        (UNIT_STAGE / 'SHA256SUMS').write_bytes(manifest.encode())
        files['SHA256SUMS'] = UNIT_STAGE / 'SHA256SUMS'
        for name, path in files.items():
            run_adb('push', str(path), stage + '/' + name)
        device_command('start')
        applied = True
        port = run_adb('forward', 'tcp:0', 'tcp:80')
        if not port.isdecimal():
            raise RuntimeError('ADB port unavailable')
        check(cell_online, 110, 'baseline 4G')
        env = dict(os.environ, NCC_BASE_URL='http://127.0.0.1:' + port, NCC_FIELD_SSID=ssid, NCC_FIELD_PSK=wifi_password)
        if args.wrong_password_only:
            env['NCC_FIELD_CASE'] = 'wrong-password'
        command = ['npx.cmd' if os.name == 'nt' else 'npx', 'playwright', 'test', 'uplink-field.spec.ts']
        log = ROOT / 'tmp' / 'uplink-field-browser.txt'
        with log.open('wb') as output:
            subprocess.run(command, cwd=ROOT / 'frontend', env=env, stdout=output, stderr=subprocess.STDOUT, check=True, timeout=150)
        if args.wrong_password_only:
            print('PASS: wrong password stayed unconfirmed and was cancelled in the browser', flush=True)
            check(cell_online, 100, '4G fallback after cancellation')
            verify_cell()
            return
        print('PASS: browser submitted the exact hidden SSID and confirmed its address', flush=True)
        check(wifi_online, 80, 'hidden Wi-Fi DNS/HTTPS')
        if fixture({'radio': 'off'}) != {'phase': 'radio', 'state': 'off'}:
            raise RuntimeError('AP shutdown not confirmed')
        check(cell_online, 120, '4G fallback after exiting the Wi-Fi area')
        verify_cell()
        if fixture({'radio': 'on'}) != {'phase': 'radio', 'state': 'on'}:
            raise RuntimeError('AP return not confirmed')
        check(wifi_online, 350, 'automatic reconnect to the saved hidden SSID')
        if fixture({'radio': 'block-internet'}) != {'phase': 'internet', 'state': 'blocked', 'radio': 'on'}:
            raise RuntimeError('Wi-Fi Internet outage not isolated')
        check(cell_online, 160, '4G fallback even with the Wi-Fi radio still in range')
        verify_cell()
    finally:
        try:
            if applied:
                device_command('restore')
        finally:
            if port and port.isdecimal():
                run_adb('forward', '--remove', 'tcp:' + port)
            if fixture_in:
                completed = fixture({'radio': 'done'}) if fixture_ready else json.loads(fixture_out.readline())
                if completed.get('phase') == 'error':
                    completed = json.loads(fixture_out.readline())
                if completed.get('phase') != 'complete' or completed.get('restored') is not True:
                    raise RuntimeError('USB fixture cleanup not verified: ' + str(completed))
                print('PASS: USB Wi-Fi restored, protected interfaces and host routing unchanged', flush=True)
            if sftp and fixture_dir:
                for name in ('usb-uplink-fixture.py', 'usb-wifi-ap-probe.py'):
                    sftp.remove(fixture_dir + '/' + name)
                sftp.rmdir(fixture_dir)
                sftp.close()
            ssh.close()


if __name__ == '__main__':
    main()

import argparse
import http.cookiejar
import json
import re
import subprocess
import time
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description='Reconcile the verified NP1 STA mismatch through its normal Web API')
    parser.add_argument('--serial', required=True)
    parser.add_argument('--staged-script')
    parser.add_argument('--cycle', action='store_true', help='Rearm STA once and let automatic mode fall back to 4G')
    parser.add_argument('--verify-only', action='store_true', help='Verify NP1-origin cellular DNS and HTTPS without changing settings')
    args = parser.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9._:-]+', args.serial) or (
        not args.verify_only and not re.fullmatch(
            r'/run/np1-uplink-tools\.[A-Za-z0-9]{6}/uplink-recovery\.sh', args.staged_script or ''
        )
    ):
        parser.error('Invalid ADB serial or staged script')

    adb = ['adb', '-s', args.serial]

    def command(*arguments):
        return subprocess.check_output(adb + list(arguments), text=True, timeout=36).strip()

    def shell(arguments):
        return command('shell', arguments)

    def native_value():
        return shell('od -An -j332 -N4 -tu4 /tmp/victoria/victoria_PMEM_AL_SRV_SET_SETTING_ITEM.map').strip()

    def verify_cellular():
        status = json.loads(shell('/run/np1-control/ncc --mode cellular-internet-probe'))
        if status.get('status') != 'passed' or not all(status.get(key) is True for key in (
            'dnsVerified', 'tlsVerified', 'httpVerified', 'identityUnchanged'
        )):
            raise SystemExit('Interface-bound cellular probe did not pass')
        print('4G DNS and certificate-verified HTTPS confirmed on ' + status['interface'], flush=True)

    if args.verify_only:
        verify_cellular()
        return

    port = command('forward', 'tcp:0', 'tcp:80')
    if not port.isdecimal():
        raise SystemExit('ADB did not return a local forward port')
    base = 'http://127.0.0.1:' + port
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                        urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    csrf = ''

    def api(path, data=None):
        request = urllib.request.Request(base + '/api/v1' + path,
            data=None if data is None else json.dumps(data).encode(),
            headers={'Origin': base, 'X-CSRF-Token': csrf, 'X-Request-ID': str(uuid.uuid4()),
                     'Content-Type': 'application/json'})
        with client.open(request, timeout=25) as response:
            return json.load(response)

    actions = '/device/native-operations/actions/'

    def switch(value):
        reply = api(actions + 'wifi-switch', {'confirmation': '純正Wi-FiのON/OFFを変更', 'values': {'setting': value}})
        if reply['values'].get('result') != 1 or reply['values'].get('setting') != value:
            raise SystemExit('Switch response not confirmed; no automatic resend')

    try:
        status = api(actions + 'wifi-status')['values']
        uplink = api('/network/uplink')
        if status.get('result') != 1 or status.get('setting') != 2 or native_value() != '0':
            raise SystemExit('Expected pure STA OFF / saved STA ON mismatch was not verified')
        if uplink.get('mode') != 'auto' or (uplink.get('status') or {}).get('uplink') != 'none':
            raise SystemExit('Expected automatic mode with no uplink was not verified')
        snapshot = shell('sh ' + args.staged_script + ' snapshot')
        if 'STA native before: 0' not in snapshot or 'Recovery snapshot ready' not in snapshot:
            raise SystemExit('Device pre-change snapshot not verified')
        print(snapshot, flush=True)
        csrf = api('/auth/session')['csrf']
        if args.cycle:
            try:
                switch(1)
            except urllib.error.HTTPError as error:
                if error.code != 503:
                    raise
                print('ON response unconfirmed; reading owner state without resending', flush=True)
            confirmation_deadline = time.monotonic() + 12
            while time.monotonic() < confirmation_deadline:
                readback = api(actions + 'wifi-status')['values']
                if readback.get('result') == 1 and readback.get('setting') == 1 and native_value() == '0':
                    break
                time.sleep(1)
            else:
                raise SystemExit('ON read-back did not match; no automatic resend')
            print('STA ON confirmed; allowing automatic mode to fall back to 4G', flush=True)
        else:
            switch(2)
            print('Stock Communication accepted STA OFF; awaiting native and 4G read-back', flush=True)
        deadline = time.monotonic() + (260 if args.cycle else 65)
        while time.monotonic() < deadline:
            route = shell('ip -4 route get 1.1.1.1 2>/dev/null || true')
            match = re.search(r'\bdev (usb0(?:\.[0-9]+)?|rmnet[a-zA-Z0-9_.]*)\b', route)
            if native_value() == '1' and match and re.search(r'\bsrc \d+\.\d+\.\d+\.\d+', route):
                print('STA OFF stored; external IPv4 route uses ' + match[1], flush=True)
                verify_cellular()
                return
            time.sleep(2)
        raise SystemExit('Saved STA state and external cellular route did not become ready; no automatic resend')
    except urllib.error.HTTPError as error:
        raise SystemExit(f'NP1CC API returned HTTP {error.code}; no automatic resend') from None
    finally:
        command('forward', '--remove', 'tcp:' + port)


if __name__ == '__main__':
    main()

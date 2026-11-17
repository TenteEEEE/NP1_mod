"""Forward local HTTPS and record the certificate fingerprint obtained over physical ADB."""
import argparse
import hashlib
import json
import pathlib
import re
import ssl
import subprocess
parser = argparse.ArgumentParser()
parser.add_argument('--serial', required=True)
parser.add_argument('--port', type=int, default=443)
parser.add_argument('--http-port', type=int, default=80)
root = pathlib.Path(__file__).resolve().parent.parent
parser.add_argument('--access-file', type=pathlib.Path, default=root / 'tmp/device-access.json')
args = parser.parse_args()
if not re.fullmatch(r'[A-Za-z0-9._:-]+', args.serial) or not all(1 <= port <= 65535 for port in (args.port, args.http_port)) or args.port == args.http_port:
    raise SystemExit('Invalid serial or ports')
adb = ['adb', '-s', args.serial]
subprocess.run(adb + ['forward', 'tcp:' + str(args.port), 'tcp:443'], check=True)
subprocess.run(adb + ['forward', 'tcp:' + str(args.http_port), 'tcp:80'], check=True)
pem = subprocess.check_output(adb + ['shell', 'cat', '/overlay/owner-apps/np1-control/config/tls.pem']).decode().replace('\r', '')
fingerprint = hashlib.sha256(ssl.PEM_cert_to_DER_cert(pem)).hexdigest()
data = {'url': 'https://127.0.0.1' + ((':' + str(args.port)) if args.port != 443 else ''), 'httpURL': 'http://127.0.0.1' + ((':' + str(args.http_port)) if args.http_port != 80 else ''), 'fingerprint': fingerprint}
args.access_file.parent.mkdir(parents=True, exist_ok=True)
args.access_file.write_text(json.dumps(data, indent=2), encoding='utf8')
print(data['url'])
print('TLS SHA256:', fingerprint)
print('HTTP:', data['httpURL'], '(no Web password)')

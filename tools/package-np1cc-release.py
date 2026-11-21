"""Sign a Linux/ARM64 NP1CC installation bundle and optional compatible update."""

import argparse
import gzip
import hashlib
import json
import re
import subprocess
import sys
import zipfile
from pathlib import Path, PurePosixPath

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from full_install_contract import missing_full_install_files


ROOT = Path(__file__).resolve().parents[1]
ALLOWED_ROOTS = {'bin', 'experimental', 'licenses', 'plate', 'systemd', 'tools', 'vpn'}
ALLOWED_FILES = {'LICENSE', 'THIRD_PARTY_LICENSES.md', 'SHA256SUMS', 'build-info.txt', 'dependency-inventory.json'}


def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def signed(output: Path, name: str, data: dict, key: Ed25519PrivateKey) -> None:
    raw = json.dumps(data, indent=2, sort_keys=True).encode('utf-8') + b'\n'
    (output / name).write_bytes(raw)
    (output / f'{name}.sig').write_bytes(key.sign(raw))


def staged_files(bundle: Path) -> list[tuple[PurePosixPath, Path]]:
    files = []
    for line in (bundle / 'SHA256SUMS').read_text(encoding='ascii').splitlines():
        digest, separator, filename = line.partition('  ')
        relative = PurePosixPath(filename)
        if (not separator or not re.fullmatch('[0-9a-f]{64}', digest) or not relative.parts
                or relative.is_absolute() or '..' in relative.parts or '\\' in filename
                or (relative.parts[0] not in ALLOWED_ROOTS and filename not in ALLOWED_FILES)):
            raise ValueError(f'Unexpected bundle file: {filename}')
        path = bundle.joinpath(*relative.parts)
        if not path.is_file() or path.is_symlink() or not path.resolve().is_relative_to(bundle.resolve()):
            raise ValueError(f'Unexpected bundle file type: {filename}')
        if sha(path.read_bytes()) != digest:
            raise ValueError(f'Bundle checksum mismatch: {filename}')
        files.append((relative, path))
    files.append((PurePosixPath('SHA256SUMS'), bundle / 'SHA256SUMS'))
    return files


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--key', type=Path, required=True, help='Ed25519 PKCS#8 PEM stored outside Git')
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--from-binary', type=Path, action='append', default=[],
                        help='Installed ncc binary tested for Web/broker/worker and state compatibility (repeatable)')
    args = parser.parse_args()
    key = serialization.load_pem_private_key(args.key.read_bytes(), password=None)
    if not isinstance(key, Ed25519PrivateKey):
        parser.error('expected an Ed25519 signing key')
    version = json.loads((ROOT / 'frontend/package.json').read_bytes())['version']
    source = (ROOT / 'backend/internal/update/release.go').read_text(encoding='utf8')
    if not re.fullmatch(r'(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)', version) or not re.search(rf'\bVersion\s*=\s*"{re.escape(version)}"', source):
        parser.error('frontend and backend release versions differ')
    bundle = ROOT / 'artifacts/ncc'
    missing = missing_full_install_files(bundle)
    if missing:
        parser.error(f'full device build required: {", ".join(missing)}')
    binary_path = bundle / 'bin/ncc'
    binary = binary_path.read_bytes()
    contract = json.loads(subprocess.check_output([sys.executable, str(ROOT / 'tools/verify-recorder-artifacts.py'),
                                                   '--json', '--binary', str(binary_path)]))
    files = staged_files(bundle)
    if args.out.exists() and any(args.out.iterdir()):
        parser.error('release output directory must be empty')
    args.out.mkdir(parents=True, exist_ok=True)
    (args.out / 'np1cc-update-ed25519.pub').write_text(key.public_key().public_bytes_raw().hex() + '\n', encoding='ascii')
    name = f'np1cc-install-v{version}-linux-arm64.zip'
    archive = args.out / name
    with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=6) as release:
        for relative, path in files:
            release.write(path, str(relative))
    signed(args.out, 'np1cc-install.json', {
        'schema': 1, 'version': version, 'target': 'linux/arm64', 'asset': name,
        'size': archive.stat().st_size, 'sha256': sha(archive.read_bytes()),
    }, key)
    if not args.from_binary:
        print(f'NP1CC v{version} bootstrap package created')
        return
    compatible = set()
    for old in args.from_binary:
        previous = old.read_bytes()
        if previous == binary or any(value.encode('ascii') not in previous for value in contract.values()):
            parser.error(f'update base must be an earlier NCC build with the same recorder contract: {old.name}')
        compatible.add(sha(previous))
    drive_rec = json.loads((ROOT / 'artifacts/recorder-hook/build-info.json').read_text(encoding='utf-8'))
    asset = 'np1cc-update-linux-arm64.gz'
    compressed = gzip.compress(binary, compresslevel=6, mtime=0)
    if len(binary) > 48 << 20 or len(compressed) > 32 << 20:
        parser.error('binary or compressed size exceeds the device updater limit')
    (args.out / asset).write_bytes(compressed)
    signed(args.out, 'np1cc-update.json', {
        'schema': 1, 'product': 'np1cc', 'target': 'linux/arm64', 'version': version, 'minUpdater': 1,
        'fromSha256': sorted(compatible), 'driveRecSha256': drive_rec['pinnedDriveRecSha256'],
        'hookSha256': contract['hookSha256'], 'injectorSha256': contract['injectorSha256'],
        'asset': asset, 'assetSize': len(compressed), 'assetSha256': sha(compressed),
        'binarySize': len(binary), 'binarySha256': sha(binary),
    }, key)
    print(f'NP1CC v{version} bootstrap and compatible update packages created')


if __name__ == '__main__':
    main()

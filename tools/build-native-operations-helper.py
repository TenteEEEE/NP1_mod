"""Test and build the fixed native operations helper; never deploy or execute it."""
import argparse
import hashlib
import json
import pathlib
import shutil
import subprocess
import sys

from elftools.elf.elffile import ELFFile

ROOT = pathlib.Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--clang', default=shutil.which('clang') or r'C:\Program Files\LLVM\bin\clang.exe')
parser.add_argument('--test-only', action='store_true')
args = parser.parse_args()
common = [args.clang, '-O2', '-Wall', '-Wextra', '-Werror', '-ffreestanding', '-fno-builtin', '-fno-stack-protector', '-fuse-ld=lld']
wasm = ROOT / 'tmp/native-operations-frame.wasm'
subprocess.run(common + ['--target=wasm32', '-nostdlib', str(ROOT / 'tools/native/test-native-operations.c'),
    '-Wl,--no-entry', '-Wl,--export-memory'] + ['-Wl,--export=ncc_ops_' + name for name in ['buffer', 'parse', 'value', 'seen', 'raw', 'action_count', 'action']] + ['-o', str(wasm)], check=True)
subprocess.run(['node', str(ROOT / 'tools/native/test-native-operations.cjs'), str(wasm)], check=True)
if args.test_only:
    sys.exit(0)
material = ROOT / 'resources/native-sysroot'
for name in ('libc.so.6', 'libdl.so.2', 'libpthread.so.0'):
    if not (material / name).is_file():
        raise SystemExit('Missing owner-device link material: ' + name)
output = ROOT / 'artifacts/native-operations-helper'
output.mkdir(parents=True, exist_ok=True)
binary = output / 'ncc-native-operations-helper'
source = ROOT / 'tools/native/native-operations-helper.c'
subprocess.run(common + ['--target=aarch64-linux-gnu', '-nostdlib', '-fPIE', '-pie', str(source),
    str(ROOT / 'tools/native/start-aarch64.S'), '-Wl,--no-as-needed', str(material / 'libdl.so.2'),
    str(material / 'libpthread.so.0'), str(material / 'libc.so.6'), '-Wl,--allow-shlib-undefined',
    '-Wl,--export-dynamic', '-Wl,-e,_start', '-Wl,--dynamic-linker=/lib/ld-linux-aarch64.so.1',
    '-Wl,-z,relro', '-Wl,-z,now', '-o', str(binary)], check=True)
with binary.open('rb') as stream:
    image = ELFFile(stream)
    needed = [item.needed for item in image.get_section_by_name('.dynamic').iter_tags('DT_NEEDED')]
    if any(any(part in name for part in ('NStandard', 'AplLib', 'AIPI')) for name in needed):
        raise SystemExit('Vendor linked before preflight')
    symbols = {item.name for item in image.get_section_by_name('.dynsym').iter_symbols() if item['st_value']}
    for name in ('_Z23RemoveAllTemporaryFilesv', '_ZN17AL_SettingService13EnableSettingEv',
                 'open', 'semget', 'semctl', 'semop', 'usleep', 'pthread_cond_wait', 'pthread_cond_timedwait',
                 'pthread_mutex_lock', 'pthread_mutex_unlock', 'pthread_mutex_timedlock', 'pthread_create',
                 '_Z12NSystemErrorjPKcmmmb', '_ZN17NProcSharedMemory4OpenEPKDs', '_ZN17NProcSharedMemory10FreeMemoryEv',
                 'fchown', 'fchmod', 'ftruncate', 'unlink', '_ZN17NProcSharedMemory5AllocEPKDsl'):
        if name not in symbols:
            raise SystemExit('Required process-local guard missing: ' + name)
    if image['e_machine'] != 'EM_AARCH64':
        raise SystemExit('Incorrect architecture')
sources = [ROOT / 'tools/native' / name for name in ('native-operations-helper.c', 'operations-frame.h', 'operations-actions.h',
    'native-alexa-runtime.h', 'native-core.h', 'vendor-fingerprints.h', 'vendor-fingerprints.json', 'vendor-slots.h', 'start-aarch64.S')]
info = {'architecture': 'aarch64-linux-gnu', 'sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
        'needed': needed, 'sources': {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sources},
        'compiler': subprocess.check_output([args.clang, '--version'], text=True).splitlines()[0], 'executedOnDevice': False}
(output / 'build-info.json').write_text(json.dumps(info, indent=2) + '\n', encoding='utf8')
print('PASS: operations ELF, vendor-free startup, required initialization/receiver guards; not deployed')

"""Build and validate the pinned DriveRec startup hook; never deploy it."""
import argparse, hashlib, json, pathlib, shutil, subprocess
from elftools.elf.elffile import ELFFile

ROOT=pathlib.Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--clang',default=shutil.which('clang') or r'C:\Program Files\LLVM\bin\clang.exe');p.add_argument('--drive-rec',type=pathlib.Path,default=ROOT/'resources/stock-service-inventory/DriveRec');p.add_argument('--test-only',action='store_true');a=p.parse_args()
stock=a.drive_rec.read_bytes()
stock_sha=hashlib.sha256(stock).hexdigest()
expected_stock_sha='000871dc36581f27ae831d9db3772b553a17cce0d814c52867276ac68ea0cde0'
expected_instructions={0x138378:'e91340d13fd907f9fd7bbba9fd030091',0x1384d8:'e91340d13fd107f9fd7bbaa9e403012a',0x138658:'e91340d13fd907f9fd7bbba9640300d0'}
if stock_sha!=expected_stock_sha:raise SystemExit('DriveRec SHA-256 does not match the pinned firmware')
if stock[:8]!=b'\x7fELF\x02\x01\x01\x03':raise SystemExit('DriveRec ELF identity mismatch')
for offset,expected in expected_instructions.items():
 if stock[offset:offset+16].hex()!=expected:raise SystemExit(f'DriveRec instruction mismatch at 0x{offset:x}')
with a.drive_rec.open('rb') as stream:
 stock_elf=ELFFile(stream);stock_symbols={x.name for x in stock_elf.get_section_by_name('.dynsym').iter_symbols() if x['st_shndx']=='SHN_UNDEF'}
 if not any(x.startswith('_Z31NPMPS_CreateVideoEncodeStreamer') for x in stock_symbols):raise SystemExit('DriveRec streamer import missing')
common=[a.clang,'-O2','-Wall','-Wextra','-Werror','-ffreestanding','-fno-builtin','-fno-stack-protector','-fuse-ld=lld']
wasm=ROOT/'tmp/recorder-hook-config.wasm'
subprocess.run(common+['--target=wasm32','-nostdlib',str(ROOT/'tools/native/test-recorder-hook-config.c'),'-Wl,--no-entry','-Wl,--export-memory','-Wl,--export=ncc_recorder_buffer','-Wl,--export=ncc_recorder_crc','-Wl,--export=ncc_recorder_decode','-o',str(wasm)],check=True)
subprocess.run(['node',str(ROOT/'tools/native/test-recorder-hook-config.cjs'),str(wasm)],check=True)
if a.test_only:raise SystemExit
material=ROOT/'resources/native-sysroot'
for name in ('libc.so.6','libdl.so.2'):
 if not (material/name).is_file():raise SystemExit('Missing owner-device link material: '+name)
out=ROOT/'artifacts/recorder-hook';out.mkdir(parents=True,exist_ok=True);library=out/'libncc-driverec-hook.so'
subprocess.run(common+['--target=aarch64-linux-gnu','-nostdlib','-fPIC','-shared',str(ROOT/'tools/native/recorder-hook.c'),str(material/'libdl.so.2'),str(material/'libc.so.6'),'-Wl,--allow-shlib-undefined','-Wl,-z,relro','-Wl,-z,now','-Wl,-soname,libncc-driverec-hook.so','-o',str(library)],check=True)
with library.open('rb') as stream:
 image=ELFFile(stream);dynamic=image.get_section_by_name('.dynamic');needed=[x.needed for x in dynamic.iter_tags('DT_NEEDED')];symbols={x.name for x in image.get_section_by_name('.dynsym').iter_symbols() if x['st_value']}
 if image['e_machine']!='EM_AARCH64':raise SystemExit('Incorrect architecture')
 if not any('NPMPS_CreateVideoEncodeStreamer' in x for x in symbols):raise SystemExit('Streamer interposition symbol missing')
 if any('AplLib' in x or 'NStandard' in x for x in needed):raise SystemExit('Vendor library linked into startup hook')
 if any(x.entry.d_tag=='DT_TEXTREL' for x in dynamic.iter_tags()):raise SystemExit('Text relocations are forbidden')
 if not image.get_section_by_name('.init_array') or not image.get_section_by_name('.init_array').data():raise SystemExit('Startup constructor missing')
 for segment in image.iter_segments():
  if segment['p_type']=='PT_GNU_STACK' and segment['p_flags']&1:raise SystemExit('Executable stack is forbidden')
sources=[ROOT/'tools/native'/x for x in ('recorder-hook.c','recorder-hook-config.h','native-core.h','test-recorder-hook-config.c','test-recorder-hook-config.cjs')]
info={'architecture':'aarch64-linux-gnu','sha256':hashlib.sha256(library.read_bytes()).hexdigest(),'needed':needed,'pinnedDriveRecSha256':stock_sha,'verifiedInstructionOffsets':[f'0x{x:x}' for x in expected_instructions],'sources':{str(x.relative_to(ROOT)):hashlib.sha256(x.read_bytes()).hexdigest() for x in sources},'compiler':subprocess.check_output([a.clang,'--version'],text=True).splitlines()[0],'executedOnDevice':False}
(out/'build-info.json').write_text(json.dumps(info,indent=2)+'\n',encoding='utf8');print('PASS: pinned DriveRec hook, config parser and interposition symbol; not deployed')

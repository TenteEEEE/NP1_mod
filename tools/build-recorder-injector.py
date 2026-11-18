"""Build the pinned AArch64 DriveRec runtime injector; never deploy it."""
import argparse, hashlib, json, pathlib, shutil, subprocess
from elftools.elf.elffile import ELFFile

ROOT=pathlib.Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--clang',default=shutil.which('clang') or r'C:\Program Files\LLVM\bin\clang.exe');a=p.parse_args()
hook=ROOT/'artifacts/recorder-hook/libncc-driverec-hook.so'
if not hook.is_file():raise SystemExit('Build recorder hook first')
with hook.open('rb') as stream:
 hook_elf=ELFFile(stream);reapply=next((x['st_value'] for x in hook_elf.get_section_by_name('.dynsym').iter_symbols() if x.name=='ncc_recorder_reapply'),0)
if not reapply:raise SystemExit('Hook runtime reapply symbol missing')
source=(ROOT/'tools/native/recorder-injector.c').read_text(encoding='utf8').replace('@HOOK_SHA@',hashlib.sha256(hook.read_bytes()).hexdigest()).replace('@REAPPLY_OFFSET@',hex(reapply))
generated=ROOT/'tmp/recorder-injector.generated.c';generated.write_text(source,encoding='utf8')
material=ROOT/'resources/native-sysroot';out=ROOT/'artifacts/recorder-injector';out.mkdir(parents=True,exist_ok=True);binary=out/'ncc-recorder-injector'
command=[a.clang,'-O2','-Wall','-Wextra','-Werror','--target=aarch64-linux-gnu','-nostdlib','-fPIE','-pie','-fuse-ld=lld','-I',str(ROOT/'tools/native'),str(generated),str(ROOT/'tools/native/start-aarch64.S'),str(material/'libdl.so.2'),str(material/'libc.so.6'),'-Wl,-e,_start','-Wl,--dynamic-linker=/lib/ld-linux-aarch64.so.1','-Wl,-z,relro','-Wl,-z,now','-o',str(binary)]
subprocess.run(command,check=True)
with binary.open('rb') as stream:
 image=ELFFile(stream);dynamic=image.get_section_by_name('.dynamic');needed=[x.needed for x in dynamic.iter_tags('DT_NEEDED')]
 if image['e_machine']!='EM_AARCH64':raise SystemExit('Incorrect architecture')
 for segment in image.iter_segments():
  if segment['p_type']=='PT_GNU_STACK' and segment['p_flags']&1:raise SystemExit('Executable stack is forbidden')
info={'architecture':'aarch64-linux-gnu','sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'hookSha256':hashlib.sha256(hook.read_bytes()).hexdigest(),'reapplyOffset':hex(reapply),'needed':needed,'sourceSha256':hashlib.sha256((ROOT/'tools/native/recorder-injector.c').read_bytes()).hexdigest(),'compiler':subprocess.check_output([a.clang,'--version'],text=True).splitlines()[0],'executedOnDevice':False}
(out/'build-info.json').write_text(json.dumps(info,indent=2)+'\n',encoding='utf8');print('PASS: pinned DriveRec runtime injector; not deployed')

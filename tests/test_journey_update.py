"""Isolated update/rollback filesystem fixtures; never contact a device."""
import hashlib
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
BASH = r'C:\Program Files\Git\bin\bash.exe' if os.name == 'nt' else shutil.which('bash')


@unittest.skipUnless(BASH and pathlib.Path(BASH).exists(), 'Bash required')
class JourneyUpdateTests(unittest.TestCase):
    def fixture(self, scenario='success', history=True):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = pathlib.Path(temporary.name)
        for name in ('base/bin', 'base/state', 'base/config', 'base/recovery', 'stage/bin', 'stage/tools', 'mock', 'run'):
            (root/name).mkdir(parents=True, exist_ok=True)
        (root/'base/bin/ncc').write_bytes(b'old-binary\n')
        (root/'stage/bin/ncc').write_bytes(b'#!/bin/sh\n# new-binary\necho "$NCC_FIXTURE/base/state"\n')
        if history:
            (root/'base/state/radio-observations.jsonl').write_bytes(b'original-radio\n')
            (root/'base/state/radio-logger.json').write_bytes(b'original-logger\n')
        (root/'base/config/config.json').write_text('{"stateDir":"' + (root/'base/state').as_posix() + '"}', encoding='utf8')
        (root/'active').write_text('active')
        mocks = {
            'id': '#!/bin/sh\necho 0\n',
            'chown': '#!/bin/sh\nexit 0\n',
            'python3': '#!/bin/sh\nexec "$NCC_PYTHON" "$@"\n',
            'systemctl': '''#!/bin/sh
echo "$*" >> "$NCC_FIXTURE/commands"
case "$1" in
 is-active) if test "$3" = np1-control.service; then test "$(cat "$NCC_FIXTURE/active")" = active; else exit 0; fi ;;
 stop) echo stopped > "$NCC_FIXTURE/active" ;;
 start|restart) echo active > "$NCC_FIXTURE/active" ;;
 show)
   for arg in "$@"; do
     case "$arg" in *.service)
       pid=100
       if test "$arg" = app-@Voice.service && test "$NCC_SCENARIO" = drift && test -f "$NCC_FIXTURE/changed"; then pid=101; fi
       printf 'Id=%s\\nMainPID=%s\\nInvocationID=fixed\\nActiveState=active\\nSubState=running\\n' "$arg" "$pid"
     ;; esac
   done ;;
esac
'''
        }
        for name, content in mocks.items():
            p=root/'mock'/name
            p.write_text(content, encoding='utf8', newline='\n')
            p.chmod(0o755)
        scripts = {
            'web-worker-update.sh': '''#!/bin/sh
set -eu
cp bin/ncc "$NCC_FIXTURE/base/bin/ncc"
cp bin/ncc "$NCC_FIXTURE/run/ncc"
touch "$NCC_FIXTURE/changed"
systemctl restart np1-control-broker.service
systemctl restart np1-control.service
''',
            'repair-radio-history.sh': '''#!/bin/sh
set -eu
echo repaired > "$NCC_FIXTURE/base/state/radio-observations.jsonl"
if test "$NCC_SCENARIO" = repair-fails; then exit 1; fi
echo '{"updated":1}'
''',
            'verify.sh': '#!/bin/sh\nsystemctl is-active --quiet np1-control.service\n'
        }
        for name, content in scripts.items():
            (root/'stage/tools'/name).write_text(content, encoding='utf8', newline='\n')
        content=(ROOT/'tools/device/journey-update.sh').read_text(encoding='utf8')
        content=content.replace('/overlay/owner-apps/np1-control', (root/'base').as_posix()).replace('/run/np1-control/ncc', (root/'run/ncc').as_posix())
        content=content.replace('set -eu', 'set -eu\nPATH="$(CDPATH= cd -- "$NCC_FIXTURE/mock" && pwd):$PATH"\nexport PATH', 1)
        entry=root/'stage/tools/journey-update.sh'
        entry.write_text(content, encoding='utf8', newline='\n')
        files=[p for p in (root/'stage').rglob('*') if p.is_file()]
        (root/'stage/SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.relative_to(root/'stage').as_posix()+'\n' for p in files), encoding='ascii', newline='\n')
        env=dict(os.environ, NCC_FIXTURE=root.as_posix(), NCC_SCENARIO=scenario, NCC_PYTHON=pathlib.Path(sys.executable).as_posix())
        result=subprocess.run([BASH,entry.as_posix()],env=env,capture_output=True,timeout=30)
        self.assertTrue((root/'commands').exists(),result.stderr.decode(errors='replace'))
        commands=(root/'commands').read_text().splitlines()
        for command in commands:
            if command.split()[0] in ('start','stop','restart'):
                self.assertNotIn('app-@',command)
                self.assertNotIn('qmmf',command)
                self.assertNotIn('np1-control-ap.',command)
                self.assertNotIn('np1-control-modem.',command)
                self.assertNotIn('np1-control-native.',command)
                self.assertNotIn('np1-control-plate.',command)
        return root,result

    def test_success_keeps_exact_original_history_backup(self):
        root,result=self.fixture()
        self.assertEqual(result.returncode,0,result.stderr.decode(errors='replace'))
        self.assertIn(b'PASS: journey update',result.stdout)
        backup=next((root/'base/recovery').iterdir())
        self.assertEqual((backup/'radio-observations.jsonl').read_bytes(),b'original-radio\n')
        self.assertEqual((root/'base/bin/ncc').read_bytes(),(root/'stage/bin/ncc').read_bytes())
        self.assertEqual((root/'active').read_text().strip(),'active')

    def test_failed_repair_restores_binary_and_both_logger_files(self):
        root,result=self.fixture('repair-fails')
        self.assertNotEqual(result.returncode,0)
        self.assertEqual((root/'base/bin/ncc').read_bytes(),b'old-binary\n')
        self.assertEqual((root/'base/state/radio-observations.jsonl').read_bytes(),b'original-radio\n')
        self.assertEqual((root/'base/state/radio-logger.json').read_bytes(),b'original-logger\n')
        self.assertEqual((root/'active').read_text().strip(),'active')

    def test_unrelated_service_drift_fails_without_restarting_it(self):
        root,result=self.fixture('drift')
        self.assertNotEqual(result.returncode,0)
        self.assertEqual((root/'base/bin/ncc').read_bytes(),b'old-binary\n')

    def test_failed_update_preserves_original_absence_of_logs(self):
        root,result=self.fixture('repair-fails',history=False)
        self.assertNotEqual(result.returncode,0)
        self.assertEqual(list((root/'base/state').iterdir()),[])

    def repair_fixture(self, fail_readback=False, action='apply'):
        root,result=self.fixture()
        self.assertEqual(result.returncode,0,result.stderr.decode(errors='replace'))
        original=b'{"at":"2026-11-15T00:00:00Z","rsrp":-85}\n'
        history=root/'base/state/radio-observations.jsonl'
        history.write_bytes(original)
        (root/'ncc_mock.py').write_text('''import json,os,pathlib,sys
root=pathlib.Path(os.environ['NCC_FIXTURE'])
if '--radio-repair-state-dir' in sys.argv:
 print((root/'base/state').as_posix());sys.exit(0)
p=root/'base/state/radio-observations.jsonl'
value=json.loads(p.read_text())
matched=int('gps' not in value)
if '--radio-repair-apply' in sys.argv:
 value['gps']={'lat':35,'lon':139};p.write_text(json.dumps(value)+'\\n')
 (root/'base/state/journey-tracks').mkdir(exist_ok=True)
 (root/'base/state/journey-tracks/2026-11-13.json.gz').write_bytes(b'new-retained-track')
if '--radio-repair-check' in sys.argv and (matched or os.environ.get('NCC_REPAIR_FAIL')=='1'):
 sys.exit(4)
print(json.dumps({'matched':matched,'updated':matched if '--radio-repair-apply' in sys.argv else 0}))
''',encoding='utf8',newline='\n')
        binary=root/'run/ncc'
        binary.write_text('#!/bin/sh\nexec "$NCC_PYTHON" "$NCC_FIXTURE/ncc_mock.py" "$@"\n',encoding='utf8',newline='\n')
        binary.chmod(0o755)
        content=(ROOT/'tools/device/repair-radio-history.sh').read_text(encoding='utf8')
        content=content.replace('/overlay/owner-apps/np1-control',(root/'base').as_posix()).replace('/run/np1-control/ncc',binary.as_posix())
        content=content.replace('set -eu','set -eu\nPATH="$(CDPATH= cd -- "$NCC_FIXTURE/mock" && pwd):$PATH"\nexport PATH',1)
        entry=root/'real-repair.sh';entry.write_text(content,encoding='utf8',newline='\n')
        env=dict(os.environ,NCC_FIXTURE=root.as_posix(),NCC_SCENARIO='success',NCC_REPAIR_FAIL='1' if fail_readback else '0',NCC_PYTHON=pathlib.Path(sys.executable).as_posix())
        result=subprocess.run([BASH,entry.as_posix(),action],env=env,capture_output=True,timeout=30)
        self.assertEqual((root/'active').read_text().strip(),'active')
        return root,result,original

    def test_repair_plan_is_read_only_and_uses_runtime_binary(self):
        root,result,original=self.repair_fixture(action='plan')
        self.assertEqual(result.returncode,0,result.stderr.decode(errors='replace'))
        self.assertEqual((root/'base/state/radio-observations.jsonl').read_bytes(),original)
        self.assertFalse((root/'base/private').exists())

    def test_repair_apply_backs_up_original_and_checks_readback(self):
        root,result,original=self.repair_fixture()
        self.assertEqual(result.returncode,0,result.stderr.decode(errors='replace'))
        backup=next((root/'base/private').iterdir())
        self.assertEqual((backup/'radio-observations.jsonl').read_bytes(),original)
        self.assertIn(b'"gps"',(root/'base/state/radio-observations.jsonl').read_bytes())

    def test_repair_readback_failure_restores_original(self):
        root,result,original=self.repair_fixture(fail_readback=True)
        self.assertNotEqual(result.returncode,0)
        self.assertEqual((root/'base/state/radio-observations.jsonl').read_bytes(),original)
        self.assertFalse((root/'base/state/journey-tracks').exists())


if __name__ == '__main__':
    unittest.main()

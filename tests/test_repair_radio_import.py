"""Measured-track imports must work after reboot without a deployed /tmp bundle."""
import contextlib
import hashlib
import importlib.util
import io
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = pathlib.Path(__file__).resolve().parents[1]


class RepairImportTests(unittest.TestCase):
    def run_import(self, corrupt_script=False):
        spec = importlib.util.spec_from_file_location('repair_radio_import', ROOT/'tools/repair-radio-history.py')
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = pathlib.Path(temporary.name)
        (root/'tools/device').mkdir(parents=True)
        (root/'tmp').mkdir()
        (root/'tools/device/repair-radio-history.sh').write_bytes(b'#!/bin/sh\r\nexit 0\r\n')
        source = root/'measured.json'
        source.write_text(json.dumps({'status':'available','data':{'source':'stock-recording-nmea','points':[]}}))
        module.__file__ = str(root/'tools/repair-radio-history.py')
        remote = {}
        executions = []

        def run(args, **kwargs):
            command = args[3:]
            if command == ['shell','id -u']:
                return subprocess.CompletedProcess(args, 0, b'0\n', b'')
            if command[0] == 'push':
                remote[command[2]] = pathlib.Path(command[1]).read_bytes()
                return subprocess.CompletedProcess(args, 0, b'transferred\n', b'')
            self.assertEqual(command[0], 'shell')
            self.assertNotIn('/tmp/ncc-stage/', command[1])
            self.assertIn('/bin/sh /tmp/ncc-radio-repair.sh apply', command[1])
            self.assertEqual(remote['/tmp/ncc-radio-repair.sh'], b'#!/bin/sh\nexit 0\n')
            executions.append(command[1])
            return subprocess.CompletedProcess(args, 0, b'{"matched":0}\n{"matched":0}\nNCC_RADIO_REPAIR_EXIT=0\n', b'')

        def check_output(args, **kwargs):
            self.assertEqual(args[3:5], ['shell','sha256sum'])
            path = args[5]
            digest = hashlib.sha256(remote[path]).hexdigest()
            if corrupt_script and path.endswith('.sh'):
                digest = '0'*64
            return (digest+'  '+path+'\n').encode()

        with patch.object(sys, 'argv', ['repair-radio-history.py','--serial','synthetic-test-transport','--measured-track',str(source)]), patch.object(module.subprocess, 'run', side_effect=run), patch.object(module.subprocess, 'check_output', side_effect=check_output), contextlib.redirect_stdout(io.StringIO()):
            if corrupt_script:
                with self.assertRaisesRegex(RuntimeError, 'Repair script transfer read-back mismatch'):
                    module.main()
                self.assertEqual(executions, [])
            else:
                module.main()
                self.assertEqual(len(executions), 1)

    def test_import_stages_verified_script_without_previous_deployment(self):
        self.run_import()

    def test_script_hash_mismatch_prevents_device_mutation(self):
        self.run_import(corrupt_script=True)


if __name__ == '__main__':
    unittest.main()

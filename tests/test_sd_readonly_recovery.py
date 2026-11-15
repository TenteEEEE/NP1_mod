"""Exercise the exact bounded reader used on NP1 against synthetic regular files."""
import io
import os
import pathlib
import sys
import tempfile
import types
import unittest
from unittest.mock import patch

ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = (ROOT/'tools/device/read-sd-sectors.sh').read_text(encoding='utf8')
READER = SCRIPT.split("python3 -c '\n", 1)[1].split("\n' \"$device\"", 1)[0]


def sentence(body):
    checksum = 0
    for value in body.encode():
        checksum ^= value
    return ('$'+body+'*%02X\r\n' % checksum).encode()


class SDReadOnlyRecoveryTests(unittest.TestCase):
    def reader(self, raw, ranges, mode='--batch', dates='141126'):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        path = pathlib.Path(temporary.name)/'synthetic-recording-sd'
        path.write_bytes(raw)
        output = io.BytesIO()
        original_open = os.open
        opens = []

        def read_only_open(name, flags, *args):
            opens.append(flags)
            self.assertEqual(flags, os.O_RDONLY)
            # NP1 is POSIX; Windows fixture descriptors need binary mode to
            # reproduce its byte-exact reads rather than translate CR/LF.
            return original_open(name, flags | getattr(os, 'O_BINARY', 0), *args)

        def execute():
            with patch.object(sys, 'argv', ['reader', str(path), str(len(raw)//512), mode, dates]), patch.object(sys, 'stdin', io.StringIO(ranges)), patch.object(sys, 'stdout', types.SimpleNamespace(buffer=output)), patch.object(os, 'open', side_effect=read_only_open):
                exec(compile(READER, 'read-sd-sectors.sh', 'exec'), {})
        return path, output, opens, execute

    def test_exact_sector_reads_never_write_the_source(self):
        raw = b'a'*512+b'b'*512+b'c'*512
        path, output, opens, execute = self.reader(raw, '1 2\n')
        execute()
        self.assertEqual(output.getvalue(), b'1 2\n'+b'b'*512+b'c'*512)
        self.assertEqual(path.read_bytes(), raw)
        self.assertEqual(opens, [os.O_RDONLY])

    def test_invalid_or_out_of_bounds_ranges_return_no_sector_data(self):
        for value in ['-1 1\n', '0 4097\n', '0 0\n', '2 1\n', '0 1 extra\n']:
            with self.subTest(value=value):
                path, output, _, execute = self.reader(b'x'*1024, value)
                with self.assertRaises(ValueError):
                    execute()
                self.assertEqual(output.getvalue(), b'')
                self.assertEqual(path.read_bytes(), b'x'*1024)

    def test_nmea_filter_keeps_real_sentences_and_no_fix_breaks(self):
        valid = sentence('GPRMC,010000.00,A,3500.0000,N,13900.0000,E,10.0,90.0,141126,,,A')
        nofix = sentence('GPRMC,010001.00,V,,,,,,,141126,,,N')
        raw = b'video-prefix'+valid+nofix+b'video-tail'
        raw = raw.ljust(512, b'x')
        path, output, _, execute = self.reader(raw, '0 1\n', '--nmea')
        execute()
        header, payload = output.getvalue().split(b'\n', 1)
        self.assertEqual(header, ('0 1 %d' % len(payload)).encode())
        self.assertEqual(payload, valid.replace(b'\r\n', b'\n')+nofix.replace(b'\r\n', b'\n'))
        self.assertEqual(path.read_bytes(), raw)

    def test_invalid_checksum_or_wrong_date_is_not_recovered_as_a_fix(self):
        old = sentence('GPRMC,010000.00,A,3500.0000,N,13900.0000,E,10.0,90.0,131126,,,A')
        bad = old.replace(b'131126', b'141126')
        _, output, _, execute = self.reader((old+bad).ljust(512, b'x'), '0 1\n', '--nmea')
        execute()
        self.assertEqual(output.getvalue(), b'0 1 0\n')


if __name__ == '__main__':
    unittest.main()

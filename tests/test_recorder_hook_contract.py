import hashlib
import pathlib
import re
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
LIBRARY = ROOT / "artifacts/recorder-hook/libncc-driverec-hook.so"


class RecorderHookContractTests(unittest.TestCase):
    def test_dropin_only_adds_drive_rec_preload(self):
        dropin = (ROOT / "systemd/90-ncc-recorder-hook.conf").read_text(encoding="utf8")
        self.assertEqual(
            dropin,
            "[Service]\nRuntimeDirectory=np1-control-recorder-hook\nRuntimeDirectoryMode=0755\nExecStartPre=+/bin/cp /overlay/owner-apps/np1-control/recorder-hook/libncc-driverec-hook.so /run/np1-control-recorder-hook/libncc-driverec-hook.so\nExecStartPre=+/bin/chmod 0555 /run/np1-control-recorder-hook/libncc-driverec-hook.so\nEnvironment=LD_PRELOAD=/run/np1-control-recorder-hook/libncc-driverec-hook.so\n",
        )

    def test_install_and_recovery_own_exact_hook_paths(self):
        install = (ROOT / "tools/device/install.sh").read_text(encoding="utf8")
        rollback = (ROOT / "tools/device/rollback.sh").read_text(encoding="utf8")
        uninstall = (ROOT / "tools/device/uninstall.sh").read_text(encoding="utf8")
        path = "/etc/systemd/system/app-@DriveRec.service.d/90-ncc-recorder-hook.conf"
        self.assertIn("RECORDER_DROPIN_DIR=/etc/systemd/system/app-@DriveRec.service.d", install)
        self.assertIn('RECORDER_DROPIN="$RECORDER_DROPIN_DIR/90-ncc-recorder-hook.conf"', install)
        self.assertIn(path, rollback)
        self.assertIn(path, uninstall)
        self.assertNotRegex(install, r"systemctl\s+(?:restart|stop|start)\s+app-@DriveRec")
        self.assertNotRegex(uninstall, r"systemctl\s+(?:restart|stop|start)\s+app-@DriveRec")
        self.assertIn("recorder-hook-dropin.absent", install)
        self.assertIn("recorder-hook-library.absent", install)
        self.assertIn("recorder-hook-dropin.absent", rollback)
        self.assertIn("recorder-hook-library.absent", rollback)
        self.assertLess(rollback.index('sh "$(dirname "$0")/uninstall.sh"'), rollback.index("restore_recorder_hook\n    systemctl daemon-reload"))

    def test_owner_update_never_restarts_stock_and_has_exact_rollback(self):
        update = (ROOT / "tools/device/recorder-hook-update.sh").read_text(encoding="utf8")
        self.assertNotRegex(update, r"systemctl\s+(?:restart|stop|start)\s+app-@")
        self.assertNotRegex(update, r"systemctl\s+(?:restart|stop|start)\s+qmmf")
        self.assertIn("capture_stock > \"$BACKUP/stock-before.txt\"", update)
        self.assertIn("capture_stock > \"$BACKUP/stock-after.txt\"", update)
        self.assertIn("cmp \"$BACKUP/stock-before.txt\" \"$BACKUP/stock-after.txt\"", update)
        for name in ("ncc", "np1-control-broker.service", "90-ncc-recorder-hook.conf", "libncc-driverec-hook.so"):
            self.assertIn(name, update)

    def test_minimal_deployment_bundle_excludes_large_unrelated_material(self):
        package = (ROOT / "tools/package-recorder-hook-update.py").read_text(encoding="utf8")
        self.assertIn('"experimental/libncc-driverec-hook.so"', package)
        self.assertIn('"tools/recorder-hook-update.sh"', package)
        self.assertNotIn("plate/models", package)
        bundle = ROOT / "artifacts/ncc-recorder-hook"
        if bundle.is_dir():
            size = sum(path.stat().st_size for path in bundle.rglob("*") if path.is_file())
            self.assertLess(size, 20 * 1024 * 1024)
            self.assertFalse((bundle / "plate").exists())

    def test_broker_can_write_only_the_hook_owner_directory(self):
        unit = (ROOT / "systemd/np1-control-broker.service").read_text(encoding="utf8")
        self.assertIn(
            "ReadWritePaths=/overlay/owner-apps/np1-control/recorder-hook", unit
        )

    def test_streamer_hook_uses_the_pinned_encode_property_layout(self):
        source = (ROOT / "tools/native/recorder-hook.c").read_text(encoding="utf8")
        fields = [
            "double frame_rate;",
            "u32 width;",
            "u32 height;",
            "u32 codec_type;",
            "u32 abi_padding;",
            "u32 bitrate_type;",
            "u32 bitrate_bps;",
            "u32 keyframe_interval_ms;",
            "u32 tail_padding;",
        ]
        positions = [source.index(field) for field in fields]
        self.assertEqual(positions, sorted(positions))
        self.assertIn("property->width==1920u", source)
        self.assertIn("property->height==1080u", source)
        self.assertIn("property->codec_type==9u", source)
        self.assertIn("property->bitrate_bps==10000000u", source)
        self.assertIn("property->bitrate_bps==4000000u", source)
        self.assertIn("front encoder bitrate applied=", source)
        self.assertIn("cabin encoder bitrate applied=", source)
        self.assertNotIn("property[6]", source)

    def test_backend_defaults_match_observed_stock_encoders(self):
        source = (ROOT / "backend/internal/adapters/recorder_advanced.go").read_text(
            encoding="utf8"
        )
        self.assertIn("FrontBitrateKbps: 10000", source)
        self.assertIn("CabinBitrateKbps: 4000", source)

    def test_loaded_readback_hashes_the_runtime_copy(self):
        source = (ROOT / "backend/internal/adapters/recorder_advanced.go").read_text(
            encoding="utf8"
        )
        self.assertIn(
            "nativeFirmwareMatches(recorderHookLibrary, recorderHookLibrarySHA)", source
        )

    @unittest.skipUnless(LIBRARY.is_file(), "recorder hook has not been built")
    def test_backend_pins_the_packaged_hook_hash(self):
        source = (ROOT / "backend/internal/adapters/recorder_advanced.go").read_text(
            encoding="utf8"
        )
        match = re.search(r'recorderHookLibrarySHA\s*=\s*"([0-9a-f]{64})"', source)
        self.assertIsNotNone(match)
        self.assertEqual(match.group(1), hashlib.sha256(LIBRARY.read_bytes()).hexdigest())


if __name__ == "__main__":
    unittest.main()

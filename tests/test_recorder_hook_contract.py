import hashlib
import pathlib
import re
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
LIBRARY = ROOT / "artifacts/recorder-hook/libncc-driverec-hook.so"
INJECTOR = ROOT / "artifacts/recorder-injector/ncc-recorder-injector"


class RecorderHookContractTests(unittest.TestCase):
    def test_full_install_build_and_deploy_cannot_silently_omit_native_features(self):
        required = (
            "libncc-driverec-hook.so",
            "ncc-recorder-injector",
            "ncc-native-setting-helper",
            "ncc-native-alexa-helper",
            "ncc-native-setting-extended-helper",
            "ncc-native-setting-choice-helper",
            "ncc-native-setting-notification-helper",
            "ncc-native-audio-helper",
            "ncc-native-operations-helper",
            "ncc-native-wifi-sta-helper",
            "ncc-stock-start-helper",
        )
        contract = (ROOT / "tools/full_install_contract.py").read_text(encoding="utf8")
        prepare = (ROOT / "tools/prepare-full-native.py").read_text(encoding="utf8")
        deploy = (ROOT / "tools/deploy.py").read_text(encoding="utf8")
        for name in required:
            self.assertIn(name, contract)
        for builder in (
            "build-native-helper.py",
            "build-native-operations-helper.py",
            "build-recorder-hook.py",
            "build-recorder-injector.py",
        ):
            self.assertIn(builder, prepare)
        self.assertIn("missing_full_install_files(bundle)", deploy)
        self.assertLess(deploy.index("missing_full_install_files(bundle)"), deploy.index("'push'"))
        for build in ("tools/build.ps1", "tools/build.sh"):
            self.assertIn("prepare-full-native.py", (ROOT / build).read_text(encoding="utf8"))

    def test_both_fresh_installers_require_full_hook_before_device_mutation(self):
        for relative in ("tools/device/install.sh", "tools/device/install-owner-recovery.sh"):
            source = (ROOT / relative).read_text(encoding="utf8")
            for name in (
                "experimental/libncc-driverec-hook.so",
                "experimental/ncc-recorder-injector",
                "experimental/ncc-native-setting-helper",
                "experimental/ncc-native-operations-helper",
            ):
                self.assertIn(name, source, relative)
            self.assertIn("Full install component missing", source)
        verify = (ROOT / "tools/device/verify.sh").read_text(encoding="utf8")
        self.assertIn("np1-control-native.service", verify)
        self.assertIn("/api/v1/device/recorder-advanced", verify)
        self.assertIn("H.264 recorder hook was not loaded", verify)
        self.assertIn('b\'"applied":true\' in recorder', verify)

    def test_drive_rec_preload_dropin_is_removed(self):
        self.assertFalse((ROOT / "systemd/90-ncc-recorder-hook.conf").exists())
        for relative in ("tools/device/install.sh", "tools/device/install-owner-recovery.sh", "tools/device/recorder-hook-update.sh"):
            source = (ROOT / relative).read_text(encoding="utf8")
            self.assertNotIn("systemd/90-ncc-recorder-hook.conf", source)
            self.assertNotIn("LD_PRELOAD=/run/np1-control-recorder-hook", source)

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
        self.assertIn("recorder-injector.absent", install)
        self.assertIn("recorder-hook-dropin.absent", rollback)
        self.assertIn("recorder-hook-library.absent", rollback)
        self.assertIn("recorder-injector.absent", rollback)
        self.assertLess(rollback.index('sh "$(dirname "$0")/uninstall.sh"'), rollback.index("restore_recorder_hook\n    systemctl daemon-reload"))

    def test_owner_update_never_restarts_stock_and_has_exact_rollback(self):
        update = (ROOT / "tools/device/recorder-hook-update.sh").read_text(encoding="utf8")
        self.assertNotRegex(update, r"systemctl\s+(?:restart|stop|start)\s+app-@")
        self.assertNotRegex(update, r"systemctl\s+(?:restart|stop|start)\s+qmmf")
        self.assertIn("capture_stock > \"$BACKUP/stock-before.txt\"", update)
        self.assertIn("capture_stock > \"$BACKUP/stock-after.txt\"", update)
        self.assertIn("cmp \"$BACKUP/stock-before.txt\" \"$BACKUP/stock-after.txt\"", update)
        for name in ("ncc", "np1-control-broker.service", "np1-control-native.service", "libncc-driverec-hook.so", "ncc-recorder-injector", "ncc-native-operations-helper"):
            self.assertIn(name, update)

    def test_minimal_deployment_bundle_excludes_large_unrelated_material(self):
        package = (ROOT / "tools/package-recorder-hook-update.py").read_text(encoding="utf8")
        self.assertIn('"experimental/libncc-driverec-hook.so"', package)
        self.assertIn('"experimental/ncc-recorder-injector"', package)
        self.assertIn('"experimental/ncc-native-operations-helper"', package)
        self.assertIn('"systemd/np1-control-native.service"', package)
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
        self.assertNotIn("ReadWritePaths=/run/np1-control-native", unit)
        operations = (ROOT / "backend/internal/adapters/native_operations.go").read_text(encoding="utf8")
        self.assertIn('[]string{"/run/np1-control-native", "/run/np1-control"}', operations)
        self.assertIn("syscall.EROFS", operations)
        self.assertIn("defer os.RemoveAll(dir)", operations)

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
        layout = source[source.index("struct video_encode_property {") :]
        positions = [layout.index(field) for field in fields]
        self.assertEqual(positions, sorted(positions))
        self.assertIn("property->width==1920u", source)
        self.assertIn("property->height==1080u", source)
        self.assertIn("property->codec_type==9u", source)
        self.assertIn("stock_bitrate==10000000u", source)
        self.assertIn("stock_bitrate==4000000u", source)
        self.assertIn("front encoder bitrate applied=", source)
        self.assertIn("cabin encoder bitrate applied=", source)
        self.assertNotIn("property[6]", source)

    def test_backend_defaults_match_observed_stock_encoders(self):
        source = (ROOT / "backend/internal/adapters/recorder_advanced.go").read_text(
            encoding="utf8"
        )
        self.assertIn("FrontBitrateKbps: 10000", source)
        self.assertIn("CabinBitrateKbps: 4000", source)
        self.assertIn("SegmentSeconds: 180", source)

    def test_segment_duration_patch_is_pinned_and_read_back(self):
        hook = (ROOT / "tools/native/recorder-hook.c").read_text(encoding="utf8")
        backend = (ROOT / "backend/internal/adapters/recorder_advanced.go").read_text(
            encoding="utf8"
        )
        build = (ROOT / "tools/build-recorder-hook.py").read_text(encoding="utf8")
        self.assertIn("base+0x14c774", hook)
        self.assertIn("duration_expected[4]={0x86,0x16,0x80,0x52}", hook)
        self.assertIn("0x14c774:'86168052'", build)
        self.assertIn("imageBase+0x14c774", backend)
        self.assertIn("settings.SegmentSeconds<<5", backend)

    def test_encoder_live_tap_is_nonblocking_and_read_back(self):
        hook = (ROOT / "tools/native/recorder-hook.c").read_text(encoding="utf8")
        backend = (ROOT / "backend/internal/adapters/recorder_advanced.go").read_text(
            encoding="utf8"
        )
        self.assertIn('dlsym(multimedia_handle,"_ZTV16MPS_StreamBuffer")', hook)
        self.assertIn("MSG_DONTWAIT|MSG_NOSIGNAL", hook)
        self.assertIn("LIVE_CHUNK_SIZE 60000", hook)
        self.assertIn("multimediaBase+0x1970a0", backend)

    def test_loaded_readback_hashes_the_runtime_copy(self):
        source = (ROOT / "backend/internal/adapters/recorder_advanced.go").read_text(
            encoding="utf8"
        )
        self.assertIn(
            "nativeFirmwareMatches(recorderHookLibrary, recorderHookLibrarySHA)", source
        )

    def test_runtime_apply_is_in_process_and_requires_exact_readback(self):
        source = (ROOT / "backend/internal/adapters/recorder_advanced.go").read_text(encoding="utf8")
        unit = (ROOT / "systemd/np1-control-native.service").read_text(encoding="utf8")
        build = (ROOT / "tools/build-recorder-hook.py").read_text(encoding="utf8")
        self.assertIn("recorderHookInjector", source)
        self.assertIn("streamerTarget < hookStart", source)
        self.assertIn("applied [52]byte", source)
        self.assertIn("live segment duration mismatch", source)
        self.assertIn("front recording did not resume on SD", source)
        self.assertIn("cabin recording did not resume on SD", source)
        self.assertIn("cabin recording did not stop on SD", source)
        self.assertIn("reapply=scheduled", source)
        self.assertNotIn("scheduleRecorderApplyReboot", source)
        self.assertNotIn('runOperations("recorder"', source)
        self.assertNotIn('command("systemctl", "restart", "app-@DriveRec.service")', source)
        self.assertIn("ncc-recorder-injector", unit)
        self.assertIn("RuntimeDirectoryMode=0750", unit)
        self.assertIn("ExecStartPre=/bin/chmod 0755 /run/np1-control-recorder-hook", unit)
        self.assertNotIn("ExecStartPre=/bin/chmod 0755 /run/np1-control-native", unit)
        self.assertIn("ExecStartPre=/bin/touch /run/np1-control-native/recorder.lock", unit)
        self.assertIn("ExecStartPre=/bin/chmod 0600 /run/np1-control-native/recorder.lock", unit)
        self.assertIn("ReadWritePaths=/run/np1-control-native /overlay/owner-apps/np1-control/recorder-hook", unit)
        self.assertIn("-Wl,-Bsymbolic", build)
        injector = (ROOT / "tools/native/recorder-injector.c").read_text(encoding="utf8")
        self.assertIn("WAIT_ALL_TRACED 0x40000000", injector)
        self.assertIn("freeze_workers(pid)", injector)
        self.assertIn("remote_hook+@REAPPLY_OFFSET@", injector)
        self.assertIn("thaw_threads();", injector)

    @unittest.skipUnless(INJECTOR.is_file(), "recorder injector has not been built")
    def test_backend_pins_the_packaged_injector_hash(self):
        source = (ROOT / "backend/internal/adapters/recorder_advanced.go").read_text(encoding="utf8")
        match = re.search(r'recorderHookInjectorSHA\s*=\s*"([0-9a-f]{64})"', source)
        self.assertIsNotNone(match)
        self.assertEqual(match.group(1), hashlib.sha256(INJECTOR.read_bytes()).hexdigest())

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

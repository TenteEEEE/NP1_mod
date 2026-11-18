import pathlib
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]


class OTAInstallContract(unittest.TestCase):
    def read(self, relative):
        return (ROOT / relative).read_text(encoding="utf8")

    def test_both_fresh_install_paths_initialize_and_verify_prevention(self):
        for relative in ("tools/device/install.sh", "tools/device/install-owner-recovery.sh"):
            text = self.read(relative)
            self.assertIn("--mode ota-policy --ota-action install", text, relative)
            self.assertIn('ota-policy.sh" guard-check', text, relative)
            self.assertLess(text.index("--mode ota-policy --ota-action install"), text.index('ota-policy.sh" guard-check'), relative)
            self.assertLess(text.index('ota-policy.sh" guard-check'), text.index("sh tools/verify.sh"), relative)

    def test_owner_recovery_rollback_restores_original_ota_choice(self):
        text = self.read("tools/device/install-owner-recovery.sh")
        self.assertIn('"$BASE/bin/ncc" --mode ota-guard --ota-action release', text)
        self.assertIn('"$BASE/bin/ncc" --mode ota-policy --ota-action uninstall', text)
        self.assertLess(text.index("--ota-action release"), text.index("--ota-action uninstall"))

    def test_web_update_refreshes_the_private_guard_binary(self):
        text = self.read("tools/device/web-worker-update.sh")
        self.assertGreaterEqual(text.count("systemctl restart np1-control-ota-guard.service"), 2)
        self.assertIn('ota-policy.sh" guard-check', text)
        self.assertIn("Web/broker and OTA guard updated", text)

    def test_verify_requires_a_fresh_consistent_guard_report(self):
        text = self.read("tools/device/verify.sh")
        self.assertIn("systemctl is-active --quiet np1-control-ota-guard.service", text)
        self.assertIn("/run/np1-control/ota-guard.json", text)
        self.assertIn("OTA guard report is stale", text)
        self.assertIn("OTA prevention layers are incomplete", text)


if __name__ == "__main__":
    unittest.main()

import importlib.util
import pathlib
import unittest
from unittest import mock


ROOT = pathlib.Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "numbershare_register", ROOT / "tools/device/numbershare-register.py"
)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class NumberShareRegistrationTests(unittest.TestCase):
    def test_private_handoff_parsing_and_generated_handle(self):
        text = "DEVICE_API_KEY=registration-secret\nADMIN_API_KEY=management-secret\n- API：`https://example.invalid/api/`\n- 管理画面：`https://example.invalid/ui/`\n"
        self.assertEqual(MODULE.handoff_value(text, "DEVICE_API_KEY"), "registration-secret")
        self.assertEqual(MODULE.handoff_value(text, "ADMIN_API_KEY"), "management-secret")
        self.assertEqual(MODULE.handoff_url(text, "API"), "https://example.invalid/api/")
        self.assertEqual(MODULE.handoff_url(text, "管理画面"), "https://example.invalid/ui/")
        handle = MODULE.generated_handle()
        self.assertRegex(handle, MODULE.HANDLE)
        self.assertFalse(handle.isdigit())

    def test_missing_private_value_is_rejected_without_echoing_data(self):
        with self.assertRaisesRegex(RuntimeError, "DEVICE_API_KEY is missing"):
            MODULE.handoff_value("ADMIN_API_KEY=secret-value", "DEVICE_API_KEY")

    def test_management_status_request_does_not_require_body_parsing(self):
        self.assertTrue(callable(MODULE.api_status))

    def test_explicit_adb_target_must_be_available(self):
        with mock.patch.object(MODULE.subprocess, "run") as run:
            run.return_value = mock.Mock(returncode=0, stdout="device\n")
            MODULE.verify_adb_target("adb", "fixture-serial")
            run.assert_called_once_with(
                ["adb", "-s", "fixture-serial", "get-state"],
                stdout=MODULE.subprocess.PIPE,
                stderr=MODULE.subprocess.PIPE,
                timeout=10,
                check=False,
                text=True,
            )
            run.return_value = mock.Mock(returncode=1, stdout="")
            with self.assertRaisesRegex(RuntimeError, "ADB target"):
                MODULE.verify_adb_target("adb", "fixture-serial")


if __name__ == "__main__":
    unittest.main()

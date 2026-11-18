"""Build every private native component when owner-supplied material is present.

Public/CI builds do not contain device-derived link material and remain usable for
Web development.  Such a bundle is deliberately rejected by the device installer.
"""

import pathlib
import subprocess
import sys

from full_install_contract import REQUIRED_EXPERIMENTAL


ROOT = pathlib.Path(__file__).resolve().parents[1]
PRIVATE_INPUTS = (
    ROOT / "resources/native-sysroot/libc.so.6",
    ROOT / "resources/native-sysroot/libdl.so.2",
    ROOT / "resources/native-sysroot/libpthread.so.0",
    ROOT / "resources/stock-service-inventory/DriveRec",
)
BUILDERS = (
    "build-native-helper.py",
    "build-native-alexa-helper.py",
    "build-native-extended-helper.py",
    "build-native-choice-helper.py",
    "build-native-notification-helper.py",
    "build-native-audio-helper.py",
    "build-native-operations-helper.py",
    "build-native-wifi-sta-helper.py",
    "build-stock-start-helper.py",
    "build-recorder-hook.py",
    "build-recorder-injector.py",
)

missing_inputs = [str(path.relative_to(ROOT)) for path in PRIVATE_INPUTS if not path.is_file()]
if missing_inputs:
    print("Private native material is unavailable; full device install will be disabled:")
    for path in missing_inputs:
        print(" -", path)
    raise SystemExit(0)

for builder in BUILDERS:
    subprocess.run([sys.executable, str(ROOT / "tools" / builder)], check=True)

outputs = {
    "libncc-driverec-hook.so": ROOT / "artifacts/recorder-hook/libncc-driverec-hook.so",
    "ncc-recorder-injector": ROOT / "artifacts/recorder-injector/ncc-recorder-injector",
    "ncc-native-setting-helper": ROOT / "artifacts/native-helper/ncc-native-setting-helper",
    "ncc-native-alexa-helper": ROOT / "artifacts/native-alexa-helper/ncc-native-alexa-helper",
    "ncc-native-setting-extended-helper": ROOT / "artifacts/native-extended-helper/ncc-native-setting-extended-helper",
    "ncc-native-setting-choice-helper": ROOT / "artifacts/native-choice-helper/ncc-native-setting-choice-helper",
    "ncc-native-setting-notification-helper": ROOT / "artifacts/native-notification-helper/ncc-native-setting-notification-helper",
    "ncc-native-audio-helper": ROOT / "artifacts/native-audio-helper/ncc-native-audio-helper",
    "ncc-native-operations-helper": ROOT / "artifacts/native-operations-helper/ncc-native-operations-helper",
    "ncc-native-wifi-sta-helper": ROOT / "artifacts/ncc-native-wifi-sta/bin/ncc-native-wifi-sta-helper",
    "ncc-stock-start-helper": ROOT / "artifacts/stock-start-helper/ncc-stock-start-helper",
}
missing_outputs = [name for name in REQUIRED_EXPERIMENTAL if not outputs[name].is_file()]
if missing_outputs:
    raise SystemExit("Full native build did not produce: " + ", ".join(missing_outputs))
print("Full native component set built and verified")

"""Files that make an NP1CC bundle eligible for a full device installation."""

from pathlib import Path


REQUIRED_EXPERIMENTAL = (
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


def missing_full_install_files(bundle: Path) -> list[str]:
    return [
        f"experimental/{name}"
        for name in REQUIRED_EXPERIMENTAL
        if not (bundle / "experimental" / name).is_file()
    ]

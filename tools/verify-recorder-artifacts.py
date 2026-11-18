"""Verify that the recorder hook artifacts match the Go read-back contract."""

import hashlib
import pathlib
import re

from elftools.elf.elffile import ELFFile


ROOT = pathlib.Path(__file__).resolve().parents[1]
GO_SOURCE = ROOT / "backend/internal/adapters/recorder_advanced.go"
HOOK = ROOT / "artifacts/recorder-hook/libncc-driverec-hook.so"
INJECTOR = ROOT / "artifacts/recorder-injector/ncc-recorder-injector"


def sha256(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def go_constant(source: str, name: str) -> str:
    match = re.search(rf"\b{name}\s*=\s*([^\s]+)", source)
    if not match:
        raise SystemExit(f"Missing Go recorder constant: {name}")
    return match.group(1).strip('"')


for artifact in (HOOK, INJECTOR):
    if not artifact.is_file():
        raise SystemExit(f"Missing recorder artifact: {artifact.relative_to(ROOT)}")

source = GO_SOURCE.read_text(encoding="utf-8")
hook_sha = sha256(HOOK)
injector_sha = sha256(INJECTOR)
with HOOK.open("rb") as stream:
    image = ELFFile(stream)
    symbol = next(
        (
            item
            for item in image.get_section_by_name(".dynsym").iter_symbols()
            if item.name == "ncc_recorder_applied_status"
        ),
        None,
    )
if symbol is None:
    raise SystemExit("Recorder hook read-back symbol is missing")
status_offset = int(symbol["st_value"])

expected = {
    "recorderHookLibrarySHA": hook_sha,
    "recorderHookInjectorSHA": injector_sha,
    "recorderHookStatusOff": hex(status_offset),
}
for name, value in expected.items():
    actual = go_constant(source, name)
    if actual.lower() != value.lower():
        raise SystemExit(f"{name} mismatch: Go={actual}, artifact={value}")

print(
    "PASS: recorder artifacts match Go contract: "
    f"hook={hook_sha}, injector={injector_sha}, status={hex(status_offset)}"
)

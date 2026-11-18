"""Describe and verify the recorder artifacts built in the same invocation."""

import argparse
import hashlib
import json
import pathlib

from elftools.elf.elffile import ELFFile


ROOT = pathlib.Path(__file__).resolve().parents[1]
HOOK = ROOT / "artifacts/recorder-hook/libncc-driverec-hook.so"
INJECTOR = ROOT / "artifacts/recorder-injector/ncc-recorder-injector"


def sha256(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--json", action="store_true")
parser.add_argument(
    "--field",
    choices=("hookSha256", "injectorSha256", "statusOffset"),
)
parser.add_argument("--binary", type=pathlib.Path)
args = parser.parse_args()

for artifact in (HOOK, INJECTOR):
    if not artifact.is_file():
        raise SystemExit(f"Missing recorder artifact: {artifact.relative_to(ROOT)}")

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

contract = {
    "hookSha256": sha256(HOOK),
    "injectorSha256": sha256(INJECTOR),
    "statusOffset": hex(int(symbol["st_value"])),
}

if args.binary:
    if not args.binary.is_file():
        raise SystemExit(f"Missing NCC binary: {args.binary}")
    with args.binary.open("rb") as stream:
        ncc = ELFFile(stream)
        if ncc["e_machine"] != "EM_AARCH64":
            raise SystemExit("NCC binary is not AArch64")
    binary = args.binary.read_bytes()
    for name, value in contract.items():
        if value.encode("ascii") not in binary:
            raise SystemExit(f"NCC binary does not embed recorder contract field: {name}")

if args.json:
    print(json.dumps(contract, separators=(",", ":")))
elif args.field:
    print(contract[args.field])
else:
    suffix = f", binary={args.binary}" if args.binary else ""
    print(
        "PASS: recorder artifacts form a build-local contract: "
        f"hook={contract['hookSha256']}, injector={contract['injectorSha256']}, "
        f"status={contract['statusOffset']}{suffix}"
    )

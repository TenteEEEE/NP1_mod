"""Deploy just a rebuilt plate engine and its ONNX Runtime over root ADB."""
import argparse
import hashlib
import re
import shlex
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RECOVERY = "/overlay/owner-apps/np1-control/recovery"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--serial", required=True)
    parser.add_argument("--artifacts", default=ROOT / "artifacts/plate", type=Path)
    parser.add_argument("--prepare-detector", action="store_true", help="Compile and validate the Adreno detector cache")
    parser.add_argument("--rollback", help="Recovery directory printed by an earlier deployment")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9._:-]+", args.serial):
        parser.error("Invalid serial")
    adb = ["adb", "-s", args.serial]
    if subprocess.check_output([*adb, "shell", "id -u"], text=True).strip() != "0":
        raise SystemExit("Root ADB required")

    def shell(command):
        # Older adbd always returns host status zero for a remote shell.
        wrapped = command + "; result=$?; printf '\\nNCC_PLATE_EXIT=%s\\n' \"$result\""
        result = subprocess.run([*adb, "shell", wrapped], capture_output=True, text=True)
        output, marker, status = result.stdout.rstrip().rpartition("\nNCC_PLATE_EXIT=")
        print(output if marker else result.stdout, end="\n", flush=True)
        if result.stderr:
            print(result.stderr, end="", flush=True)
        if result.returncode or not marker or status != "0":
            raise SystemExit("Device shell action failed")

    pattern = re.escape(RECOVERY) + r"/plate-update-[A-Za-z0-9]{8}"
    if args.rollback:
        if not re.fullmatch(pattern, args.rollback):
            parser.error("Invalid recovery directory")
        shell(shlex.join(["sh", args.rollback + "/plate-engine-update.sh", "rollback", args.rollback]))
        return

    files = {
        "plate/plate-engine": args.artifacts / "plate-engine-aarch64",
        "plate/libonnxruntime.so.1.30.0": args.artifacts / "libonnxruntime.so.1.30.0",
        "np1-control-plate.service": ROOT / "systemd/np1-control-plate.service",
        "plate-engine-update.sh": ROOT / "tools/device/plate-engine-update.sh",
    }
    if not all(f.is_file() for f in files.values()):
        parser.error("Build the device engine before deployment")
    stage = subprocess.check_output(
        [*adb, "shell", "mktemp -d " + shlex.quote(RECOVERY + "/plate-update-XXXXXXXX")],
        text=True,
    ).strip()
    if not re.fullmatch(pattern, stage):
        raise SystemExit("Could not allocate device recovery directory")
    print("Recovery directory:", stage, flush=True)
    shell("mkdir " + shlex.quote(stage + "/plate"))
    checksums = []
    for name, source in files.items():
        digest = hashlib.sha256(source.read_bytes()).hexdigest()
        checksums.append(f"{digest}  {name}\n")
        subprocess.run([*adb, "push", str(source), stage + "/" + name], check=True, capture_output=True)
    (ROOT / "tmp").mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(dir=ROOT / "tmp") as local:
        manifest = Path(local) / "SHA256SUMS"
        manifest.write_bytes("".join(checksums).encode("ascii"))
        subprocess.run([*adb, "push", str(manifest), stage + "/SHA256SUMS"], check=True, capture_output=True)
    apply = ["sh", stage + "/plate-engine-update.sh", "apply", stage]
    if args.prepare_detector:
        apply.append("prepare-detector")
    shell(shlex.join(apply))


if __name__ == "__main__":
    main()

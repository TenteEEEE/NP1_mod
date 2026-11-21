"""Create the minimal, hashed bundle used only for the DriveRec hook update."""
import hashlib
import pathlib
import shutil


ROOT = pathlib.Path(__file__).resolve().parents[1]
SOURCE = ROOT / "artifacts/ncc"
OUTPUT = ROOT / "artifacts/ncc-recorder-hook"
FILES = (
    "bin/ncc",
    "experimental/libncc-driverec-hook.so",
    "experimental/ncc-recorder-injector",
    "experimental/ncc-native-operations-helper",
    "systemd/np1-control-broker.service",
    "systemd/np1-control-native.service",
    "tools/recorder-hook-update.sh",
    "tools/recorder-hook-prune.sh",
    "tools/web-worker-prune.sh",
    "tools/web-worker-update.sh",
    "tools/update-lock.sh",
    "tools/run.sh",
    "tools/verify.sh",
    "LICENSE",
    "THIRD_PARTY_LICENSES.md",
)


if OUTPUT.exists():
    shutil.rmtree(OUTPUT)
for relative in FILES:
    source = SOURCE / relative
    if not source.is_file():
        raise SystemExit(f"Missing recorder update input: {relative}")
    target = OUTPUT / relative
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source, target)
shutil.copytree(SOURCE / "licenses", OUTPUT / "licenses")

lines = []
for path in sorted(OUTPUT.rglob("*")):
    if path.is_file():
        lines.append(
            hashlib.sha256(path.read_bytes()).hexdigest()
            + "  "
            + path.relative_to(OUTPUT).as_posix()
        )
(OUTPUT / "SHA256SUMS").write_text("\n".join(lines) + "\n", encoding="ascii", newline="\n")
size = sum(path.stat().st_size for path in OUTPUT.rglob("*") if path.is_file())
if size > 20 * 1024 * 1024:
    raise SystemExit(f"Recorder update bundle unexpectedly large: {size} bytes")
print(f"Recorder hook update bundle: {len(lines)} files, {size} bytes")

"""Run an isolated accelerator probe on NP1 and verify stock camera/recording health."""
import argparse
import importlib.util
import re
import shlex
import subprocess
import time
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--serial", required=True)
    parser.add_argument("--probe", type=Path, required=True)
    parser.add_argument("--model", type=Path)
    parser.add_argument("--inputs", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--library", type=Path, action="append", default=[])
    parser.add_argument("--mode", choices=("cpu", "buffer", "image"), default="buffer")
    parser.add_argument("--precision", choices=("fp16", "fp32"), default="fp16")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9._:-]+", args.serial):
        parser.error("Invalid device serial")
    if bool(args.model) != bool(args.inputs) or bool(args.inputs) != bool(args.output):
        parser.error("Provide the model, inputs, and output together")

    spec = importlib.util.spec_from_file_location("plate_bench", Path(__file__).with_name("bench-plate-engine.py"))
    bench = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(bench)
    device = bench.Device(args.serial)
    before = bench.stock_snapshot(device)
    if not before["frontExportUpdating"]:
        raise SystemExit("Stock front camera export is stale")
    staged = []
    try:
        runtime = bench.make_device_dir(device, "/run/np1-plate-accelerator-XXXXXXXX")
        staged.append(runtime)
        probe = runtime + "/probe"
        device.push(args.probe, probe)
        for library in args.library:
            device.push(library, runtime + "/" + library.name)
        arguments = [probe]
        if args.model:
            models = bench.make_device_dir(device, "/overlay/owner-apps/np1-control/.plate-accelerator-XXXXXXXX")
            staged.append(models)
            model, inputs, output = (models + "/" + f for f in ("model", "inputs", "output"))
            device.push(args.model, model)
            device.push(args.inputs, inputs)
            arguments.extend([model, inputs, output, args.mode, args.precision])
        command = (
            f"chmod 0755 {shlex.quote(probe)} && ("
            "if timeout -t 1 true 2>/dev/null; then set -- timeout -t 90; else set -- timeout 90; fi; "
            f"LD_LIBRARY_PATH={shlex.quote(runtime)} systemd-run --scope --quiet "
            f'-p CPUQuota=150% -p MemoryLimit=384M nice -n 10 "$@" {shlex.join(arguments)}); '
            "result=$?; printf '\\nNCC_ACCELERATOR_RESULT=%s\\n' \"$result\""
        )
        adb = ["adb", "-s", args.serial]
        try:
            res = subprocess.run([*adb, "shell", command], text=True, capture_output=True, timeout=120)
        except subprocess.TimeoutExpired:
            raise RuntimeError("Accelerator probe timed out") from None
        log, marker, status = res.stdout.rstrip().rpartition("NCC_ACCELERATOR_RESULT=")
        print(log.strip(), flush=True)
        if res.returncode or not marker or status != "0":
            if res.stderr:
                print(res.stderr.strip(), flush=True)
            raise RuntimeError(f"Accelerator probe failed (remote status {status!r})")
        if args.output:
            args.output.parent.mkdir(parents=True, exist_ok=True)
            subprocess.run([*adb, "pull", output, str(args.output)], check=True, capture_output=True)
    finally:
        for path in reversed(staged):
            _, result = device.shell("rm -rf -- " + shlex.quote(path) + " && echo cleaned")
            if result.strip() != "cleaned":
                print("Accelerator staging cleanup failed", flush=True)
        time.sleep(2)
        problems = bench.compare_stock(before, bench.stock_snapshot(device))
        if problems:
            raise RuntimeError("Stock check failed: " + ", ".join(problems))
        print("Stock health check passed", flush=True)


if __name__ == "__main__":
    main()

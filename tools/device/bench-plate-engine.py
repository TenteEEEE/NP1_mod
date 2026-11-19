"""Compare NP1 plate backends on the same inputs and verify stock health.

Models are staged on the owner overlay and executables in /run. Both staging
directories are unique to this run and removed on exit. The report contains
timings and agreement counts only. Raw inference output stays in memory.
"""
import argparse
import json
import re
import shlex
import statistics
import struct
import subprocess
import sys
import tempfile
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tools" / "plate"))
from bench_inference import Device, compare_stock, stock_snapshot

MODEL_FILES = (
    "detector.tflite", "ocr.tflite", "ocr-kana.tflite", "ocr-classification.tflite",
    "ppocrv6_rec.onnx", "ppocr-dictionary.json", "plate-model.json",
)
WIDTH, HEIGHT = 1920, 1080
FRAME_BYTES = WIDTH * HEIGHT * 3 // 2


def make_frames(path):
    if path is None:
        return b"", 0
    data = path.read_bytes()
    if not data or len(data) % FRAME_BYTES:
        raise ValueError("Input must contain complete 1920x1080 NV12 frames")
    count = len(data) // FRAME_BYTES
    if count > 64:
        raise ValueError("Use at most 64 frames per run")
    packets = []
    for seq in range(count):
        packets.append(struct.pack("<4sIIIQ", b"NPF1", WIDTH, HEIGHT, 1, seq + 1))
        packets.append(data[seq * FRAME_BYTES:(seq + 1) * FRAME_BYTES])
    return b"".join(packets), count


def run_engine(device, adb, path, models, runtime, options, *, frames=None):
    args = [
        path, "--detector", models + "/detector.tflite", "--ocr", models + "/ocr.tflite",
        "--config", models + "/plate-model.json", *options,
    ]
    log = runtime + "/engine.log"
    command = (f"LD_LIBRARY_PATH={shlex.quote(runtime)} exec {shlex.join(args)} "
               f"< {shlex.quote(frames or '/dev/null')} 2> {shlex.quote(log)}")
    result = subprocess.run(
        [*adb, "exec-out", command], capture_output=True, timeout=180,
    )
    _, detail = device.shell("cat " + shlex.quote(log))
    try:
        rows = [json.loads(line) for line in result.stdout.splitlines()]
    except (ValueError, UnicodeError) as exc:
        raise RuntimeError("Invalid engine JSON stream") from exc
    if result.returncode or not rows or not rows[0].get("ready"):
        message = rows[0].get("error", "") if rows else ""
        raise RuntimeError(f"Engine did not start ({result.returncode}): {message}\n{detail[-2400:].strip()}")
    return rows[0], rows[1:], detail


def summarize_frames(rows, count):
    if len(rows) != count or any(r.get("seq") != i or r.get("error") for i, r in enumerate(rows, 1)):
        raise RuntimeError("Engine did not finish the complete frame sequence")
    times = [r["ms"] for r in rows]
    readings = [
        sorted(tuple(p["reading"].get(key, "") for key in ("region", "class", "kana", "number"))
               for p in row["plates"])
        for row in rows
    ]
    data = {
        "frames": count, "positiveFrames": sum(bool(r) for r in readings),
        "readableFrames": sum(any(reading[3] for reading in frame) for frame in readings),
        "medianMs": round(statistics.median(times), 2),
        "positiveMedianMs": round(statistics.median(
            row["ms"] for row in rows if row["plates"]), 2) if any(readings) else None,
        "maxMs": round(max(times), 2),
        "runtimeBackends": sorted({r["backend"] for r in rows if "backend" in r}),
    }
    return data, readings


def make_device_dir(device, template):
    code, out = device.shell("mktemp -d " + shlex.quote(template))
    path = out.strip()
    if code or not re.fullmatch(re.escape(template[:-8]) + r"[A-Za-z0-9]{8}", path):
        raise RuntimeError("Could not allocate device staging directory")
    return path


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--serial", required=True)
    p.add_argument("--engine", type=Path, required=True)
    p.add_argument("--baseline", type=Path)
    p.add_argument("--runtime", type=Path, default=ROOT / "prebuilt/plate-engine/libonnxruntime.so.1.30.0")
    p.add_argument("--models", type=Path, default=ROOT / "models/plate")
    p.add_argument("--frames", type=Path, help="A sequence of raw 1920x1080 NV12 frames")
    p.add_argument("--runs", type=int, choices=range(1, 61), default=8, metavar="1..60")
    p.add_argument("--detector-backend", action="append", choices=("cpu", "gpu"))
    p.add_argument("--cache-detector", action="store_true", help="Compare GPU detection from its program cache")
    p.add_argument("--report", type=Path, required=True)
    args = p.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9._:-]+", args.serial):
        p.error("Invalid serial")
    frames, count = make_frames(args.frames)
    local_files = [args.engine, args.runtime, *(args.models / f for f in MODEL_FILES)]
    if args.baseline:
        local_files.append(args.baseline)
    if not all(f.is_file() for f in local_files):
        p.error("Engine, runtime or model file is missing")

    adb = ["adb", "-s", args.serial]
    device = Device(args.serial)
    before = stock_snapshot(device)
    if not before["frontExportUpdating"]:
        raise SystemExit("Stock front camera export is stale")
    report = {"cases": [], "stockProblems": []}
    reference = None
    stage = []
    try:
        models = make_device_dir(device, "/overlay/owner-apps/np1-control/.plate-bench-XXXXXXXX")
        stage.append(models)
        runtime = make_device_dir(device, "/run/np1-plate-bench-XXXXXXXX")
        stage.append(runtime)
        for name in MODEL_FILES:
            device.push(args.models / name, models + "/" + name)
        device.push(args.engine, runtime + "/candidate")
        device.push(args.runtime, runtime + "/libonnxruntime.so.1")
        packed_frames = None
        if count:
            args.report.parent.mkdir(parents=True, exist_ok=True)
            with tempfile.TemporaryDirectory(dir=args.report.parent) as local:
                packets = Path(local) / "frames.npf"
                packets.write_bytes(frames)
                packed_frames = models + "/frames.npf"
                device.push(packets, packed_frames)
        if args.baseline:
            device.push(args.baseline, runtime + "/baseline")
        code, _ = device.shell(f"chmod 0755 {runtime}/candidate" + (f" {runtime}/baseline" if args.baseline else ""))
        if code:
            raise RuntimeError("Could not prepare engine executables")
        cases = [("candidate-cpu", runtime + "/candidate", ["--backend", "cpu"])]
        if args.baseline:
            cases.insert(0, ("baseline", runtime + "/baseline", []))
        for backend in args.detector_backend or ["cpu"]:
            cases.append(("gpu-ocr/" + backend, runtime + "/candidate",
                          ["--detector-backend", backend]))
        if args.cache_detector:
            cache = models + "/opencl-cache"
            code, _ = device.shell("mkdir " + shlex.quote(cache))
            if code:
                raise RuntimeError("Could not create OpenCL test cache")
            info, _, _ = run_engine(device, adb, runtime + "/candidate", models, runtime,
                                    ["--prepare-gpu-cache", cache])
            report["cachePreparationMs"] = info["initMs"]
            print("Detector cache preparation passed", flush=True)
            cases.append(("gpu-ocr/cached-detector", runtime + "/candidate", ["--gpu-cache", cache]))
        for name, path, options in cases:
            info, rows, diagnostics = run_engine(device, adb, path, models, runtime,
                                                 [*options, "--bench", str(args.runs)])
            if len(rows) != 1 or "bench" not in rows[0]:
                raise RuntimeError("Engine returned no benchmark timings")
            row = {"case": name, "backend": info["backend"], **rows[0]["bench"]}
            if "initMs" in info:
                row["initMs"] = round(info["initMs"], 2)
            row["diagnostics"] = diagnostics.splitlines()[-35:]
            if count:
                _, rows, _ = run_engine(device, adb, path, models, runtime, options, frames=packed_frames)
                data, readings = summarize_frames(rows, count)
                if reference is None:
                    reference = readings
                data["equalFrames"] = sum(a == b for a, b in zip(reference, readings))
                row["recognition"] = data
            report["cases"].append(row)
            print(json.dumps({k: v for k, v in row.items() if k != "diagnostics"}), flush=True)
    finally:
        for path in reversed(stage):
            code, _ = device.shell("rm -rf -- " + shlex.quote(path))
            if code:
                report["stockProblems"].append("Staging cleanup failed")
        time.sleep(2)
        report["stockProblems"].extend(compare_stock(before, stock_snapshot(device)))
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    if report["stockProblems"]:
        raise SystemExit("Stock health comparison failed")
    print("Stock health check passed")


if __name__ == "__main__":
    main()

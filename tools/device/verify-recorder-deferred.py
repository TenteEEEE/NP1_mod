#!/usr/bin/env python3
"""Verify save-for-next-start without changing the selected recorder values."""

import argparse
import http.cookiejar
import json
import ssl
import subprocess
import time
import urllib.request
import urllib.error
import uuid


class Client:
    def __init__(self, base):
        self.base = base
        context = ssl._create_unverified_context()
        self.opener = urllib.request.build_opener(urllib.request.HTTPSHandler(context=context), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def request(self, path, method="GET", body=None, csrf=""):
        data = None if body is None else json.dumps(body, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        headers = {"Origin": self.base}
        if data is not None:
            headers.update({"Content-Type": "application/json; charset=utf-8", "X-CSRF-Token": csrf, "X-Request-ID": str(uuid.uuid4())})
        operation = urllib.request.Request(self.base + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(operation, timeout=30) as response:
                return json.loads(response.read().decode("utf-8"))
        except urllib.error.HTTPError as error:
            detail = error.read().decode("utf-8", errors="replace")
            raise RuntimeError("HTTP {}: {}".format(error.code, detail)) from error


def drive_rec_pid(serial):
    value = subprocess.check_output(
        ["adb", "-s", serial, "shell", "systemctl", "show", "app-@DriveRec.service", "-p", "MainPID", "--value"],
        text=True,
    ).strip()
    if not value.isdigit() or int(value) <= 1:
        raise RuntimeError("DriveRec PID unavailable")
    return int(value)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--serial", required=True)
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--wait-seconds", type=float, default=7)
    args = parser.parse_args()
    base = args.base_url.rstrip("/")
    client = Client(base)
    session = client.request("/api/v1/auth/session")
    before = client.request("/api/v1/device/recorder-advanced")
    pid_before = drive_rec_pid(args.serial)
    deferred = None
    pending = None
    try:
        deferred = client.request(
            "/api/v1/device/recorder-advanced",
            "PUT",
            {"expectedGeneration": before["generation"], "settings": before["settings"], "applyNow": False, "confirmation": "", "acknowledgeRecordingGap": False},
            session["csrf"],
        )
        time.sleep(args.wait_seconds)
        pending = client.request("/api/v1/device/recorder-advanced")
        pid_pending = drive_rec_pid(args.serial)
        if deferred.get("applied") or not deferred.get("restartRequired") or pending.get("applied") or not pending.get("restartRequired") or pid_pending != pid_before:
            raise RuntimeError("deferred settings did not remain pending on the current DriveRec process")
    finally:
        restored = before
        if deferred is not None:
            last_error = None
            for _ in range(15):
                current = client.request("/api/v1/device/recorder-advanced")
                try:
                    restored = client.request(
                        "/api/v1/device/recorder-advanced",
                        "PUT",
                        {"expectedGeneration": current["generation"], "settings": before["settings"], "applyNow": True, "confirmation": "即時適用する", "acknowledgeRecordingGap": True},
                        session["csrf"],
                    )
                    break
                except RuntimeError as error:
                    last_error = error
                    time.sleep(1)
            else:
                raise last_error
    if not restored.get("applied") or restored.get("restartRequired") or restored.get("settings") != before.get("settings"):
        raise RuntimeError("original recorder settings were not restored and verified")
    print(json.dumps({"beforeGeneration": before["generation"], "deferredGeneration": deferred["generation"], "pendingAfterSeconds": args.wait_seconds, "pidUnchanged": True, "restoredGeneration": restored["generation"], "restoredApplied": True}, ensure_ascii=False))


if __name__ == "__main__":
    main()

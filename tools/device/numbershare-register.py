#!/usr/bin/env python3
"""Register one NumberShare user/device and install its issued credentials.

Secrets are read from the ignored handoff, written only to an ignored owner
record, and never printed.
"""

import argparse
import datetime as dt
import http.cookiejar
import json
import os
import pathlib
import re
import secrets
import ssl
import subprocess
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import uuid


HANDLE = re.compile(r"^[a-z0-9][a-z0-9_-]{2,23}$")


def handoff_value(text: str, name: str) -> str:
    match = re.search(rf"(?m)^{re.escape(name)}=(.+)$", text)
    if not match or not match.group(1).strip():
        raise RuntimeError(f"{name} is missing from the private handoff")
    return match.group(1).strip()


def handoff_url(text: str, label: str) -> str:
    match = re.search(rf"(?m)^- {re.escape(label)}：`(https://[^`]+)`$", text)
    if not match:
        raise RuntimeError(f"{label} URL is missing from the private handoff")
    return match.group(1)


def api_request(base: str, path: str, key: str, method: str = "GET", body=None):
    target = urllib.parse.urljoin(base.rstrip("/") + "/", path)
    data = None if body is None else json.dumps(body, ensure_ascii=False).encode("utf-8")
    headers = {"Accept": "application/json", "Connection": "close", "X-API-Key": key}
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(target, data=data, method=method, headers=headers)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return response.status, json.load(response)
    except urllib.error.HTTPError as error:
        try:
            detail = json.load(error)
        except Exception:
            detail = {}
        reason = detail.get("error") or detail.get("reason") or "request rejected"
        raise RuntimeError(f"NumberShare API HTTP {error.code}: {reason}") from None


def api_status(base: str, path: str, key: str) -> int:
    target = urllib.parse.urljoin(base.rstrip("/") + "/", path)
    request = urllib.request.Request(
        target,
        method="GET",
        headers={"Accept": "application/json", "Connection": "close", "X-API-Key": key},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return response.status
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"NumberShare management API HTTP {error.code}") from None


def atomic_private_json(path: pathlib.Path, value) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    try:
        os.chmod(temporary, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as output:
            json.dump(value, output, ensure_ascii=False, indent=2)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
        os.chmod(path, 0o600)
    except Exception:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


def control_opener(control_url: str):
    context = ssl._create_unverified_context()  # pinned local ADB tunnel only
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(
        urllib.request.HTTPCookieProcessor(jar), urllib.request.HTTPSHandler(context=context)
    )
    with opener.open(control_url + "/api/v1/auth/session", timeout=10) as response:
        session = json.load(response)
    return opener, session["csrf"]


def control_json(opener, control_url: str, path: str, method="GET", body=None, csrf=""):
    data = None if body is None else json.dumps(body).encode("utf-8")
    headers = {"Accept": "application/json"}
    if data is not None:
        headers.update(
            {
                "Content-Type": "application/json",
                "Origin": control_url,
                "X-CSRF-Token": csrf,
                "X-Request-ID": str(uuid.uuid4()),
            }
        )
    request = urllib.request.Request(control_url + "/api/v1" + path, data=data, method=method, headers=headers)
    try:
        with opener.open(request, timeout=40) as response:
            return response.status, json.load(response)
    except urllib.error.HTTPError as error:
        try:
            detail = json.load(error)
        except Exception:
            detail = {}
        raise RuntimeError(
            f"NP1 control API HTTP {error.code}: {detail.get('reason', 'request rejected')}"
        ) from None


def generated_handle() -> str:
    return "np1_" + secrets.token_hex(4)


def verify_adb_target(adb: str, serial: str) -> None:
    result = subprocess.run(
        [adb, "-s", serial, "get-state"],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=10,
        check=False,
        text=True,
    )
    if result.returncode or result.stdout.strip() != "device":
        raise RuntimeError("specified NP1 ADB target is not available")


def main() -> None:
    root = pathlib.Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--serial", required=True)
    parser.add_argument("--adb", default="adb")
    parser.add_argument("--handoff", type=pathlib.Path, default=root / "docs_private/NumberShare-client-handoff.md")
    parser.add_argument("--credentials", type=pathlib.Path, default=root / "docs_private/numbershare-credentials.json")
    parser.add_argument("--user-handle", default="")
    parser.add_argument("--device-name", default="np1-recorder")
    parser.add_argument("--control-url", default="https://127.0.0.1")
    parser.add_argument("--enable", action="store_true")
    args = parser.parse_args()

    text = args.handoff.read_text(encoding="utf-8")
    registration_key = handoff_value(text, "DEVICE_API_KEY")
    admin_key = handoff_value(text, "ADMIN_API_KEY")
    api_base = handoff_url(text, "API")
    management_url = handoff_url(text, "管理画面")

    if args.credentials.exists():
        record = json.loads(args.credentials.read_text(encoding="utf-8"))
        account = record["device"]
        for field in ("user_handle", "device_id", "api_key", "recovery_code"):
            if not account.get(field):
                raise RuntimeError("saved credential record is incomplete")
    else:
        requested = args.user_handle.strip().lower() or generated_handle()
        if not HANDLE.fullmatch(requested) or requested.isdigit():
            raise RuntimeError("user handle must be 3-24 lowercase letters, numbers, _ or - and not digits only")
        status, created = api_request(
            api_base,
            "users",
            registration_key,
            "POST",
            {"user_handle": requested, "device_name": args.device_name},
        )
        if status != 201:
            raise RuntimeError("NumberShare user registration did not return 201")
        required = ("user_id", "user_handle", "device_id", "api_key", "recovery_code")
        if any(not created.get(field) for field in required):
            raise RuntimeError("NumberShare registration response is incomplete")
        account = {field: created[field] for field in required}
        account["device_name"] = args.device_name
        record = {
            "created_at": dt.datetime.now(dt.timezone.utc).isoformat(),
            "api": api_base,
            "management_url": management_url,
            "device": account,
            "management": {"admin_api_key": admin_key},
        }
        atomic_private_json(args.credentials, record)

    print("Verifying issued device credential", flush=True)
    _, identity = api_request(api_base, "users/me", account["api_key"])
    if identity.get("user_handle") != account["user_handle"] or identity.get("device_id") != account["device_id"]:
        raise RuntimeError("issued device credential identity did not match the registration response")
    print("Verifying management credential", flush=True)
    if api_status(api_base, "openapi.json", admin_key) != 200:
        raise RuntimeError("NumberShare management credential verification failed")

    control_url = args.control_url.rstrip("/")
    print("Installing issued credential on NP1", flush=True)
    verify_adb_target(args.adb, args.serial)
    opener, csrf = control_opener(control_url)
    _, status = control_json(opener, control_url, "/plate/share/status")
    control_json(
        opener,
        control_url,
        "/plate/share/config",
        "PUT",
        {
            "expectedRevision": status["config"]["revision"],
            "enabled": args.enable,
            "endpoint": api_base,
            "key": account["api_key"],
        },
        csrf,
    )
    _, checked = control_json(opener, control_url, "/plate/share/check", "POST", {}, csrf)
    if not checked.get("ok") or checked.get("identity", {}).get("user_handle") != account["user_handle"]:
        raise RuntimeError("NP1 did not verify the installed NumberShare device credential")

    print("NumberShare registration, device credential, and management credential verified")
    print("user_handle=" + account["user_handle"])
    print("credentials=" + str(args.credentials))


if __name__ == "__main__":
    main()

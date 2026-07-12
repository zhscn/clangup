#!/usr/bin/env python3
"""Upload and publish one assembled default-channel release."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import subprocess
import urllib.error
import urllib.request


def request_json(url: str, token: str, value: dict) -> dict:
    request = urllib.request.Request(url, data=json.dumps(value, separators=(",", ":")).encode(), headers={"authorization": f"Bearer {token}", "content-type": "application/json", "user-agent": "clangup-build/1"}, method="POST")
    try:
        with urllib.request.urlopen(request) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        raise SystemExit(f"{url}: {error.code}: {error.read().decode(errors='replace')}") from error


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--endpoint", required=True)
    parser.add_argument("--token", default=os.environ.get("CLANGUP_UPLOAD_TOKEN"))
    parser.add_argument("--release", required=True, type=Path)
    args = parser.parse_args()
    if not args.token:
        raise SystemExit("upload token is required")
    release = json.loads(args.release.read_text(encoding="utf-8"))
    root = args.release.resolve().parent
    objects = []
    paths = {}
    for target in release["artifacts"]:
        for name, content_type in (("artifact", "application/zstd"), ("manifest", "application/json")):
            descriptor = target[name]
            relative = Path(descriptor["key"]).relative_to(f"releases/{release['release']['channel']}/{release['release']['version']}-{release['release']['release']}")
            local = root / relative
            paths[descriptor["key"]] = local
            objects.append({**descriptor, "content_type": content_type, "cache_control": "public, max-age=31536000, immutable"})
    result = request_json(args.endpoint.rstrip("/") + "/v1/uploads:presign", args.token, {"schema": "clangup.upload-presign/v1", "objects": objects})
    for action in result.get("objects", []):
        if action.get("status") == "already_present":
            continue
        if action.get("status") != "upload" or action.get("key") not in paths:
            raise SystemExit("invalid upload response")
        command = ["curl", "--fail", "--silent", "--show-error", "--request", "PUT"]
        for name, value in action["headers"].items():
            command.extend(["--header", f"{name}: {value}"])
        command.extend(["--upload-file", str(paths[action["key"]]), action["url"]])
        subprocess.run(command, check=True)
    published = request_json(args.endpoint.rstrip("/") + "/v1/releases:publish", args.token, {"schema": "clangup.release-publish/v1", "release": release})
    print(json.dumps(published, sort_keys=True))


if __name__ == "__main__":
    main()

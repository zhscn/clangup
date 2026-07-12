#!/usr/bin/env python3
"""Upload and publish one assembled channel release."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import subprocess
import urllib.error
import urllib.request


def request_json(url: str, token: str, value: dict) -> dict:
    request = urllib.request.Request(
        url,
        data=json.dumps(value, separators=(",", ":")).encode(),
        headers={
            "authorization": f"Bearer {token}",
            "content-type": "application/json",
            "user-agent": "clangup-build/1",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(request) as response:
            result = json.load(response)
            if not isinstance(result, dict):
                raise SystemExit(f"{url}: expected a JSON object")
            return result
    except urllib.error.HTTPError as error:
        raise SystemExit(f"{url}: {error.code}: {error.read().decode(errors='replace')}") from error


def upload_object(endpoint: str, token: str, descriptor: dict, path: Path) -> None:
    for attempt in range(3):
        result = request_json(
            endpoint.rstrip("/") + "/v1/uploads:presign",
            token,
            {"schema": "clangup.upload-presign/v1", "objects": [descriptor]},
        )
        actions = result.get("objects")
        if (
            result.get("schema") != "clangup.upload-presign-response/v1"
            or not isinstance(actions, list)
            or len(actions) != 1
            or not isinstance(actions[0], dict)
            or actions[0].get("key") != descriptor["key"]
        ):
            raise SystemExit("invalid upload response")
        action = actions[0]
        if action.get("status") == "already_present":
            return
        if (
            action.get("status") != "upload"
            or action.get("method") != "PUT"
            or not isinstance(action.get("url"), str)
            or not isinstance(action.get("headers"), dict)
        ):
            raise SystemExit("invalid upload response")
        command = [
            "curl",
            "--fail",
            "--silent",
            "--show-error",
            "--request",
            "PUT",
        ]
        for name, value in action["headers"].items():
            if not isinstance(name, str) or not isinstance(value, str):
                raise SystemExit("invalid upload headers")
            command.extend(["--header", f"{name}: {value}"])
        command.extend(["--upload-file", str(path), action["url"]])
        completed = subprocess.run(command, check=False)
        if completed.returncode == 0:
            return
        if attempt == 2:
            raise SystemExit(f"upload failed after retries: {descriptor['key']}")


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
    paths = {}
    objects = []
    for target in release["artifacts"]:
        for name, content_type in (
            ("artifact", "application/zstd"),
            ("manifest", "application/json"),
        ):
            descriptor = target[name]
            release_prefix = (
                f"releases/{release['release']['channel']}/"
                f"{release['release']['version']}-{release['release']['release']}"
            )
            relative = Path(descriptor["key"]).relative_to(release_prefix)
            local = root / relative
            paths[descriptor["key"]] = local
            objects.append(
                {
                    **descriptor,
                    "content_type": content_type,
                    "cache_control": "public, max-age=31536000, immutable",
                }
            )
    for descriptor in objects:
        upload_object(
            args.endpoint,
            args.token,
            descriptor,
            paths[descriptor["key"]],
        )
    published = request_json(
        args.endpoint.rstrip("/") + "/v1/releases:publish",
        args.token,
        {"schema": "clangup.release-publish/v1", "release": release},
    )
    if (
        published.get("schema") != "clangup.release-publish-response/v1"
        or published.get("channel") != release["release"]["channel"]
        or published.get("exact")
        != f"{release['release']['version']}-{release['release']['release']}"
    ):
        raise SystemExit("invalid publish response")
    print(json.dumps(published, sort_keys=True))


if __name__ == "__main__":
    main()

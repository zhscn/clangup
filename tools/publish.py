#!/usr/bin/env python3
"""Assemble and publish a standalone tool release to R2."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import urllib.error
import urllib.request


IMMUTABLE_CACHE_CONTROL = "public, max-age=31536000, immutable"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def assemble_release(
    tool: str, version: str, targets_root: Path
) -> tuple[dict, dict[str, Path]]:
    artifacts = []
    paths: dict[str, Path] = {}
    for target_dir in sorted(p for p in targets_root.iterdir() if p.is_dir()):
        target = target_dir.name
        archive = target_dir / f"{tool}.tar.zst"
        if not archive.is_file():
            raise SystemExit(f"missing tool archive: {archive}")
        key = f"tools/{tool}/{version}/targets/{target}/{tool}.tar.zst"
        descriptor = {
            "key": key,
            "size": archive.stat().st_size,
            "sha256": sha256_file(archive),
        }
        artifacts.append({"target": target, "artifact": descriptor})
        paths[key] = archive
    if not artifacts:
        raise SystemExit(f"no target archives found under {targets_root}")
    release = {
        "schema": "clangup.tool-release/v1",
        "tool": tool,
        "version": version,
        "artifacts": artifacts,
    }
    return release, paths


def request_json(url: str, token: str, value: dict) -> dict:
    request = urllib.request.Request(
        url,
        data=json.dumps(value, separators=(",", ":")).encode(),
        headers={
            "authorization": f"Bearer {token}",
            "content-type": "application/json",
            "user-agent": "clangup-tool-publish/1",
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
        raise SystemExit(
            f"{url}: {error.code}: {error.read().decode(errors='replace')}"
        ) from error


def upload_object(
    endpoint: str, token: str, descriptor: dict, path: Path
) -> None:
    payload = {
        **descriptor,
        "content_type": "application/zstd",
        "cache_control": IMMUTABLE_CACHE_CONTROL,
    }
    for attempt in range(3):
        result = request_json(
            endpoint.rstrip("/") + "/v1/tools/uploads:presign",
            token,
            {"schema": "clangup.tool-upload-presign/v1", "objects": [payload]},
        )
        actions = result.get("objects")
        if (
            result.get("schema") != "clangup.tool-upload-presign-response/v1"
            or not isinstance(actions, list)
            or len(actions) != 1
            or not isinstance(actions[0], dict)
            or actions[0].get("key") != descriptor["key"]
        ):
            raise SystemExit("invalid tool upload response")
        action = actions[0]
        if action.get("status") == "already_present":
            return
        if (
            action.get("status") != "upload"
            or action.get("method") != "PUT"
            or not isinstance(action.get("url"), str)
            or not isinstance(action.get("headers"), dict)
        ):
            raise SystemExit("invalid tool upload response")
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


def publish_release(
    endpoint: str, token: str, release: dict, paths: dict[str, Path]
) -> dict:
    for item in release["artifacts"]:
        descriptor = item["artifact"]
        upload_object(endpoint, token, descriptor, paths[descriptor["key"]])
    return request_json(
        endpoint.rstrip("/") + "/v1/tools:publish",
        token,
        {"schema": "clangup.tool-publish/v1", "release": release},
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--tool", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--targets", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--endpoint")
    parser.add_argument("--token", default=os.environ.get("CLANGUP_UPLOAD_TOKEN"))
    args = parser.parse_args()

    release, paths = assemble_release(args.tool, args.version, args.targets)
    args.output.mkdir(parents=True, exist_ok=True)
    release_path = args.output / "release.json"
    release_path.write_text(
        json.dumps(release, indent=2) + "\n", encoding="utf-8"
    )

    if args.endpoint:
        if not args.token:
            raise SystemExit("upload token is required when --endpoint is set")
        result = publish_release(args.endpoint, args.token, release, paths)
        print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()

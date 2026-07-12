#!/usr/bin/env python3
"""Upload default-channel build objects through the repository presign service."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import urllib.request
from typing import Any


def fail(message: str) -> None:
    raise SystemExit(f"clangup default stage: {message}")


def load_json(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        fail(f"{path}: expected a JSON object")
    return value


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as file:
        for chunk in iter(lambda: file.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def object_entry(path: Path, content_type: str) -> dict[str, Any]:
    path = path.resolve()
    if path.is_symlink() or not path.is_file():
        fail(f"object is missing or unsafe: {path}")
    digest = sha256_file(path)
    return {
        "path": str(path),
        "key": f"objects/sha256/{digest}/{path.name}",
        "size": path.stat().st_size,
        "sha256": digest,
        "content_type": content_type,
        "cache_control": "public, max-age=31536000, immutable",
    }


def fragment_file(directory: Path, name: str) -> Path:
    relative = Path(name)
    if relative.is_absolute() or len(relative.parts) != 1 or relative.name != name:
        fail(f"unsafe fragment filename: {name!r}")
    return directory / relative


def upload(endpoint: str, token: str, objects: list[dict[str, Any]]) -> None:
    request_objects = [
        {key: value for key, value in item.items() if key != "path"} for item in objects
    ]
    request = urllib.request.Request(
        endpoint.rstrip("/") + "/v1/uploads:presign",
        data=json.dumps(
            {"schema": "clangup.upload-presign/v1", "objects": request_objects},
            separators=(",", ":"),
        ).encode(),
        headers={
            "authorization": f"Bearer {token}",
            "content-type": "application/json",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(request) as response:
            result = json.load(response)
    except Exception as reason:
        fail(f"presign request failed: {reason}")
    by_key = {item["key"]: item for item in objects}
    for action in result.get("objects", []):
        if action.get("status") == "already_present":
            continue
        if action.get("status") != "upload" or action.get("key") not in by_key:
            fail("presign response contains an invalid action")
        command = ["curl", "--fail", "--silent", "--show-error", "--request", "PUT"]
        for name, value in action["headers"].items():
            command.extend(["--header", f"{name}: {value}"])
        command.extend(["--upload-file", by_key[action["key"]]["path"], action["url"]])
        subprocess.run(command, check=True)

    status_request = urllib.request.Request(
        endpoint.rstrip("/") + "/v1/objects:status",
        data=json.dumps({"objects": request_objects}, separators=(",", ":")).encode(),
        headers={
            "authorization": f"Bearer {token}",
            "content-type": "application/json",
        },
        method="POST",
    )
    with urllib.request.urlopen(status_request) as response:
        status = json.load(response)
    if any(item.get("status") != "present" for item in status.get("objects", [])):
        fail("remote object verification failed")


def public_entry(item: dict[str, Any]) -> dict[str, Any]:
    return {key: item[key] for key in ("key", "size", "sha256")}


def stage_target(args: argparse.Namespace) -> dict[str, Any]:
    fragment = load_json(args.fragment)
    directory = args.fragment.resolve().parent
    artifact = object_entry(
        fragment_file(directory, fragment["artifact"]), "application/zstd"
    )
    manifest = object_entry(
        fragment_file(directory, fragment["manifest"]), "application/json"
    )
    build_record = object_entry(
        fragment_file(directory, fragment["build_record"]), "application/json"
    )
    manifest_value = load_json(Path(manifest["path"]))
    if manifest_value.get("artifact", {}).get("sha256") != artifact["sha256"]:
        fail("artifact digest does not match manifest")
    record_value = load_json(Path(build_record["path"]))
    if record_value.get("artifact_sha256") != artifact["sha256"]:
        fail("artifact digest does not match build record")
    upload(args.endpoint, args.token, [artifact, manifest, build_record])
    return {
        "schema": "clangup.staged-target/v1",
        "release": fragment["release"],
        "target": fragment["target"],
        "artifact": public_entry(artifact),
        "manifest": public_entry(manifest),
        "build_record": public_entry(build_record),
    }


def stage_inputs(args: argparse.Namespace) -> dict[str, Any]:
    lock_value = load_json(args.spec_lock)
    source = object_entry(args.source, "application/x-xz")
    if lock_value["source"]["sha256"] != source["sha256"]:
        fail("source digest does not match locked spec")
    source["key"] = (
        f"objects/sha256/{source['sha256']}/"
        f"llvm-project-{lock_value['release']['version']}.src.tar.xz"
    )
    objects = [source]
    patches = []
    for patch in lock_value["source"]["patches"]:
        path = args.bundle.resolve() / patch["path"]
        item = object_entry(path, "text/x-patch")
        if item["sha256"] != patch["sha256"]:
            fail(f"patch digest does not match locked spec: {patch['path']}")
        objects.append(item)
        patches.append(public_entry(item))
    locked_spec = object_entry(args.spec_lock, "application/json")
    objects.append(locked_spec)
    upload(args.endpoint, args.token, objects)
    return {
        "schema": "clangup.staged-inputs/v1",
        "release": lock_value["release"],
        "locked_spec": public_entry(locked_spec),
        "source": public_entry(source),
        "patches": patches,
    }


def stage_file(args: argparse.Namespace) -> dict[str, Any]:
    item = object_entry(args.file, args.content_type)
    item["key"] = args.key
    upload(args.endpoint, args.token, [item])
    return {
        "schema": "clangup.staged-file/v1",
        "object": public_entry(item),
    }


def parse_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--endpoint", required=True)
    parser.add_argument("--token", default=os.environ.get("CLANGUP_UPLOAD_TOKEN"))
    parser.add_argument("--output", required=True, type=Path)
    commands = parser.add_subparsers(dest="command", required=True)
    target = commands.add_parser("target")
    target.add_argument("--fragment", required=True, type=Path)
    inputs = commands.add_parser("inputs")
    inputs.add_argument("--spec-lock", required=True, type=Path)
    inputs.add_argument("--source", required=True, type=Path)
    inputs.add_argument("--bundle", required=True, type=Path)
    file = commands.add_parser("file")
    file.add_argument("--file", required=True, type=Path)
    file.add_argument("--key", required=True)
    file.add_argument("--content-type", required=True)
    return parser.parse_args()


def main() -> None:
    args = parse_arguments()
    if not args.token:
        fail("upload token is required")
    if args.command == "target":
        result = stage_target(args)
    elif args.command == "inputs":
        result = stage_inputs(args)
    else:
        result = stage_file(args)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(result, sort_keys=True, separators=(",", ":")) + "\n",
        encoding="utf-8",
    )


if __name__ == "__main__":
    main()

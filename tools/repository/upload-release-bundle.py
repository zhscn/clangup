#!/usr/bin/env python3
"""Import a legacy CI release bundle into the clangup R2 object layout."""

from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import subprocess
from typing import Any
import urllib.request


IMMUTABLE_CACHE = "public, max-age=31536000, immutable"


def fail(message: str) -> None:
    raise SystemExit(f"clangup bundle upload: {message}")


def load_json(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as reason:
        fail(f"cannot read {path}: {reason}")
    if not isinstance(value, dict):
        fail(f"{path}: expected a JSON object")
    return value


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as file:
        for chunk in iter(lambda: file.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def safe_file(root: Path, relative: str) -> Path:
    path = Path(relative)
    if path.is_absolute() or ".." in path.parts or path == Path("."):
        fail(f"unsafe bundle path: {relative!r}")
    result = root.joinpath(path)
    if result.is_symlink() or not result.is_file():
        fail(f"bundle file is missing or unsafe: {relative}")
    return result


def object_entry(
    path: Path, content_type: str, expected: str | None = None
) -> dict[str, Any]:
    digest = sha256_file(path)
    if expected is not None and digest != expected:
        fail(f"sha256 mismatch for {path}: expected {expected}, got {digest}")
    return {
        "path": path,
        "key": f"objects/sha256/{digest}/{path.name}",
        "size": path.stat().st_size,
        "sha256": digest,
        "content_type": content_type,
    }


class S3:
    def __init__(self, endpoint: str, bucket: str, dry_run: bool) -> None:
        self.endpoint = endpoint
        self.bucket = bucket
        self.dry_run = dry_run
        self.environment = os.environ.copy()
        self.environment.setdefault("AWS_DEFAULT_REGION", "auto")
        self.environment.setdefault("AWS_REQUEST_CHECKSUM_CALCULATION", "when_required")
        self.environment.setdefault("AWS_RESPONSE_CHECKSUM_VALIDATION", "when_required")

    def command(
        self, *arguments: str, capture: bool = False
    ) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [
                "aws",
                "s3api",
                *arguments,
                "--bucket",
                self.bucket,
                "--endpoint-url",
                self.endpoint,
                "--no-cli-pager",
            ],
            check=False,
            text=True,
            stdout=subprocess.PIPE if capture else None,
            stderr=subprocess.PIPE if capture else None,
            env=self.environment,
        )

    def head(self, key: str) -> dict[str, Any] | None:
        result = self.command(
            "head-object", "--key", key, "--output", "json", capture=True
        )
        if result.returncode != 0:
            return None
        return json.loads(result.stdout)

    def upload(self, item: dict[str, Any]) -> None:
        key = item["key"]
        existing = self.head(key) if not self.dry_run else None
        if existing is not None:
            metadata = {
                name.lower(): value
                for name, value in existing.get("Metadata", {}).items()
            }
            if (
                existing.get("ContentLength") != item["size"]
                or metadata.get("sha256") != item["sha256"]
            ):
                fail(f"remote object has a different identity: {key}")
            print(f"present  s3://{self.bucket}/{key}")
            return
        if self.dry_run:
            print(f"upload   s3://{self.bucket}/{key} <- {item['path']}")
            return
        if item["size"] >= 64 << 20:
            result = subprocess.run(
                [
                    "aws",
                    "s3",
                    "cp",
                    str(item["path"]),
                    f"s3://{self.bucket}/{key}",
                    "--endpoint-url",
                    self.endpoint,
                    "--content-type",
                    item["content_type"],
                    "--cache-control",
                    IMMUTABLE_CACHE,
                    "--metadata",
                    f"sha256={item['sha256']}",
                    "--only-show-errors",
                    "--no-progress",
                ],
                check=False,
                env=self.environment,
            )
        else:
            result = self.command(
                "put-object",
                "--key",
                key,
                "--body",
                str(item["path"]),
                "--content-type",
                item["content_type"],
                "--cache-control",
                IMMUTABLE_CACHE,
                "--metadata",
                f"sha256={item['sha256']}",
                "--if-none-match",
                "*",
            )
        if result.returncode != 0:
            fail(f"upload failed: {key}")
        existing = self.head(key)
        metadata = (
            {
                name.lower(): value
                for name, value in existing.get("Metadata", {}).items()
            }
            if existing
            else {}
        )
        if (
            existing is None
            or existing.get("ContentLength") != item["size"]
            or metadata.get("sha256") != item["sha256"]
        ):
            fail(f"remote verification failed: {key}")
        print(f"uploaded s3://{self.bucket}/{key}")


class Presign:
    def __init__(self, endpoint: str, token: str, dry_run: bool) -> None:
        self.endpoint = endpoint.rstrip("/")
        self.token = token
        self.dry_run = dry_run

    def request(self, path: str, value: object) -> dict[str, Any]:
        request = urllib.request.Request(
            self.endpoint + path,
            data=json.dumps(value, separators=(",", ":")).encode(),
            headers={
                "authorization": f"Bearer {self.token}",
                "content-type": "application/json",
            },
            method="POST",
        )
        with urllib.request.urlopen(request) as response:
            result = json.load(response)
        if not isinstance(result, dict):
            fail("presign service returned an invalid response")
        return result

    @staticmethod
    def remote(item: dict[str, Any]) -> dict[str, Any]:
        return {
            "key": item["key"],
            "size": item["size"],
            "sha256": item["sha256"],
            "content_type": item["content_type"],
            "cache_control": IMMUTABLE_CACHE,
        }

    def upload(self, item: dict[str, Any]) -> None:
        if self.dry_run:
            print(f"upload   {item['key']} <- {item['path']}")
            return
        remote = self.remote(item)
        result = self.request(
            "/v1/uploads:presign",
            {"schema": "clangup.upload-presign/v1", "objects": [remote]},
        )
        actions = result.get("objects", [])
        if len(actions) != 1:
            fail("presign service returned the wrong action count")
        action = actions[0]
        if action.get("status") == "already_present":
            print(f"present  {item['key']}")
            return
        if action.get("status") != "upload" or action.get("key") != item["key"]:
            fail(f"presign service rejected {item['key']}")
        command = ["curl", "--fail", "--silent", "--show-error", "--request", "PUT"]
        for name, value in action["headers"].items():
            command.extend(["--header", f"{name}: {value}"])
        command.extend(["--upload-file", str(item["path"]), action["url"]])
        subprocess.run(command, check=True)
        status = self.request("/v1/objects:status", {"objects": [remote]})
        if status.get("objects") != [{"key": item["key"], "status": "present"}]:
            fail(f"remote verification failed: {item['key']}")
        print(f"uploaded {item['key']}")


def public(item: dict[str, Any]) -> dict[str, Any]:
    return {name: item[name] for name in ("key", "size", "sha256")}


def prepare(root: Path, output: Path) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    bundle = load_json(root / "bundle.json")
    if bundle.get("schema") != "clangup.release-bundle/v1":
        fail("unsupported bundle schema")
    release = {
        "channel": bundle["channel"],
        "version": bundle["version"],
        "release": bundle["release"],
    }
    lock_path = safe_file(root, bundle["locked_spec"])
    locked_spec = object_entry(
        lock_path, "application/json", bundle["locked_spec_sha256"]
    )
    lock = load_json(lock_path)
    objects = [locked_spec]
    staged_objects: dict[tuple[str, str], dict[str, Any]] = {}
    for item in bundle["objects"]:
        content_type = (
            "application/x-xz" if item["kind"] == "source" else "text/x-patch"
        )
        staged = object_entry(
            safe_file(root, item["path"]), content_type, item["sha256"]
        )
        if item["kind"] == "source":
            staged["key"] = (
                f"objects/sha256/{staged['sha256']}/"
                f"llvm-project-{release['version']}.src.tar.xz"
            )
        else:
            patch = next(
                (
                    patch
                    for patch in lock["source"]["patches"]
                    if patch["sha256"] == staged["sha256"]
                ),
                None,
            )
            if patch is None:
                fail(f"bundle contains an unlocked patch: {staged['sha256']}")
            staged["key"] = (
                f"objects/sha256/{staged['sha256']}/{Path(patch['path']).name}"
            )
        objects.append(staged)
        staged_objects[(item["kind"], item["sha256"])] = staged

    artifacts = []
    for item in bundle["artifacts"]:
        triple = item["target"]
        payload = object_entry(
            safe_file(root, item["payload"]), "application/zstd", item["payload_sha256"]
        )
        manifest = object_entry(
            safe_file(root, item["manifest"]),
            "application/json",
            item["manifest_sha256"],
        )
        record_relative = item.get(
            "build_record", f"build-records/{triple}/build-record.json"
        )
        build_record = object_entry(
            safe_file(root, record_relative),
            "application/json",
            item.get("build_record_sha256"),
        )
        manifest["key"] = (
            f"objects/sha256/{manifest['sha256']}/{payload['path'].name}.manifest.json"
        )
        build_record["key"] = (
            f"objects/sha256/{build_record['sha256']}/{payload['path'].name}.build-record.json"
        )
        manifest_value = load_json(Path(manifest["path"]))
        record_value = load_json(Path(build_record["path"]))
        if (
            manifest_value.get("release") != release
            or record_value.get("release") != release
        ):
            fail(f"release identity mismatch for {triple}")
        if manifest_value.get("artifact", {}).get("sha256") != payload["sha256"]:
            fail(f"manifest artifact mismatch for {triple}")
        if record_value.get("artifact_sha256") != payload["sha256"]:
            fail(f"build record artifact mismatch for {triple}")
        objects.extend([payload, manifest, build_record])
        artifacts.append(
            {
                "target": triple,
                "artifact": public(payload),
                "manifest": public(manifest),
                "build_record": public(build_record),
            }
        )

    source_digest = lock["source"]["sha256"]
    source = staged_objects.get(("source", source_digest))
    if source is None:
        fail("bundle does not contain the locked source")
    patches = []
    for patch in lock["source"]["patches"]:
        staged = staged_objects.get(("patch", patch["sha256"]))
        if staged is None:
            fail(f"bundle does not contain patch {patch['sha256']}")
        patches.append(public(staged))
    descriptor = {
        "schema": "clangup.release/v1",
        "release": release,
        "locked_spec": public(locked_spec),
        "source": public(source),
        "patches": patches,
        "artifacts": sorted(artifacts, key=lambda item: item["target"]),
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(
        json.dumps(descriptor, sort_keys=True, separators=(",", ":")) + "\n",
        encoding="utf-8",
    )
    release_key = f"releases/{release['channel']}/{release['version']}-{release['release']}/release.json"
    release_object = object_entry(output, "application/json")
    release_object["key"] = release_key
    return objects + [release_object], descriptor


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("bundle", type=Path)
    parser.add_argument(
        "--endpoint",
        default="https://8153cf0faeab699eac694cde811fbe8d.r2.cloudflarestorage.com",
    )
    parser.add_argument("--bucket", default="clangup")
    parser.add_argument("--output", type=Path)
    parser.add_argument("--jobs", type=int, default=3)
    parser.add_argument("--presign-endpoint")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    root = args.bundle.resolve()
    output = args.output or root / "release.json"
    objects, descriptor = prepare(root, output.resolve())
    if args.presign_endpoint:
        token = os.environ.get("CLANGUP_UPLOAD_TOKEN")
        if not token and not args.dry_run:
            fail("CLANGUP_UPLOAD_TOKEN is required with --presign-endpoint")
        client: S3 | Presign = Presign(
            args.presign_endpoint, token or "dry-run", args.dry_run
        )
    else:
        client = S3(args.endpoint, args.bucket, args.dry_run)
    if args.jobs < 1:
        fail("--jobs must be positive")
    with ThreadPoolExecutor(max_workers=args.jobs) as executor:
        futures = [executor.submit(client.upload, item) for item in objects[:-1]]
        for future in futures:
            future.result()
    client.upload(objects[-1])
    print(
        f"release {descriptor['release']['channel']}@{descriptor['release']['version']}-"
        f"{descriptor['release']['release']} contains {len(descriptor['artifacts'])} artifacts"
    )


if __name__ == "__main__":
    main()

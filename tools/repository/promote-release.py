#!/usr/bin/env python3
"""Promote an uploaded release into the unsigned repository catalog."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
from typing import Any


def fail(message: str) -> None:
    raise SystemExit(f"clangup catalog promotion: {message}")


def run_aws(
    endpoint: str, bucket: str, arguments: list[str], capture: bool = False
) -> subprocess.CompletedProcess[str]:
    environment = os.environ.copy()
    environment.setdefault("AWS_DEFAULT_REGION", "auto")
    environment.setdefault("AWS_REQUEST_CHECKSUM_CALCULATION", "when_required")
    environment.setdefault("AWS_RESPONSE_CHECKSUM_VALIDATION", "when_required")
    return subprocess.run(
        [
            "aws",
            "s3api",
            *arguments,
            "--bucket",
            bucket,
            "--endpoint-url",
            endpoint,
            "--no-cli-pager",
        ],
        check=False,
        text=True,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.PIPE if capture else None,
        env=environment,
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("release", type=Path)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--display-name", required=True)
    parser.add_argument("--default-channel")
    parser.add_argument("--catalog-key", default="catalog-v1.json")
    parser.add_argument(
        "--endpoint",
        default="https://8153cf0faeab699eac694cde811fbe8d.r2.cloudflarestorage.com",
    )
    parser.add_argument("--bucket", default="clangup")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()

    release = json.loads(args.release.read_text(encoding="utf-8"))
    if release.get("schema") != "clangup.release/v1":
        fail("unsupported release descriptor")
    identity = release["release"]
    channel = identity["channel"]
    exact = f"{identity['version']}-{identity['release']}"
    descriptor = f"releases/{channel}/{exact}/release.json"
    descriptor_contents = args.release.read_bytes()
    descriptor_object = {
        "key": descriptor,
        "size": len(descriptor_contents),
        "sha256": hashlib.sha256(descriptor_contents).hexdigest(),
    }
    if not args.dry_run:
        release_head = run_aws(
            args.endpoint,
            args.bucket,
            ["head-object", "--key", descriptor, "--output", "json"],
            capture=True,
        )
        if release_head.returncode != 0:
            fail("release descriptor is not uploaded")
        release_metadata = json.loads(release_head.stdout)
        custom = {
            name.lower(): value
            for name, value in release_metadata.get("Metadata", {}).items()
        }
        if (
            release_metadata.get("ContentLength") != descriptor_object["size"]
            or custom.get("sha256") != descriptor_object["sha256"]
        ):
            fail("remote release descriptor identity differs")

    head = run_aws(
        args.endpoint,
        args.bucket,
        ["head-object", "--key", args.catalog_key, "--output", "json"],
        capture=True,
    )
    etag: str | None = None
    if head.returncode == 0:
        metadata = json.loads(head.stdout)
        etag = metadata["ETag"]
        with tempfile.NamedTemporaryFile() as file:
            result = run_aws(
                args.endpoint,
                args.bucket,
                ["get-object", "--key", args.catalog_key, file.name],
                capture=True,
            )
            if result.returncode != 0:
                fail("cannot download existing catalog")
            catalog: dict[str, Any] = json.loads(Path(file.name).read_text())
        if catalog.get("schema") != "clangup.catalog/v1":
            fail("existing catalog has an unsupported schema")
        if catalog.get("repository", {}).get("namespace") != args.namespace:
            fail("existing catalog namespace differs")
    else:
        catalog = {
            "schema": "clangup.catalog/v1",
            "repository": {
                "namespace": args.namespace,
                "display_name": args.display_name,
            },
            "channels": {},
        }
    if args.default_channel:
        catalog["repository"]["default_channel"] = args.default_channel
    entry = catalog["channels"].setdefault(channel, {"current": exact, "releases": []})
    releases = [
        item
        for item in entry["releases"]
        if not (
            item["version"] == identity["version"]
            and item["release"] == identity["release"]
        )
    ]
    releases.append(
        {
            "version": identity["version"],
            "release": identity["release"],
            "descriptor": descriptor_object,
        }
    )
    releases.sort(key=lambda item: (item["version"], item["release"]))
    entry["releases"] = releases
    entry["current"] = exact

    contents = json.dumps(catalog, sort_keys=True, separators=(",", ":")) + "\n"
    output = args.release.with_name("catalog-v1.json")
    output.write_text(contents, encoding="utf-8")
    digest = hashlib.sha256(contents.encode()).hexdigest()
    if args.dry_run:
        print(contents, end="")
        return
    condition = ["--if-match", etag] if etag else ["--if-none-match", "*"]
    result = run_aws(
        args.endpoint,
        args.bucket,
        [
            "put-object",
            "--key",
            args.catalog_key,
            "--body",
            str(output),
            "--content-type",
            "application/json",
            "--cache-control",
            "no-cache",
            "--metadata",
            f"sha256={digest}",
            *condition,
        ],
    )
    if result.returncode != 0:
        fail("catalog conditional update failed")
    print(f"promoted {channel}@{exact} in s3://{args.bucket}/{args.catalog_key}")


if __name__ == "__main__":
    main()

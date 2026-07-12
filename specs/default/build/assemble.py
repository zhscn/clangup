#!/usr/bin/env python3
"""Assemble per-target build outputs into a release bundle."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import shutil
from typing import Any


def fail(message: str) -> None:
    raise SystemExit(f"clangup default assemble: {message}")


def load_json(path: Path) -> dict[str, Any]:
    with path.open("r", encoding="utf-8") as file:
        value = json.load(file)
    if not isinstance(value, dict):
        fail(f"{path}: expected a JSON object")
    return value


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as file:
        for chunk in iter(lambda: file.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def canonical_json(value: Any) -> bytes:
    return json.dumps(
        value, sort_keys=True, separators=(",", ":"), ensure_ascii=False
    ).encode()


def safe_child(directory: Path, name: str) -> Path:
    path = Path(name)
    if path.is_absolute() or len(path.parts) != 1 or path.name != name:
        fail(f"unsafe fragment filename {name!r}")
    result = directory / path
    if result.is_symlink() or not result.is_file():
        fail(f"fragment file is missing or unsafe: {result}")
    return result


def copy_verified(source: Path, destination: Path, expected: str | None = None) -> str:
    digest = sha256_file(source)
    if expected is not None and digest != expected:
        fail(f"sha256 mismatch for {source}: expected {expected}, got {digest}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
    return digest


def parse_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--spec-lock", required=True, type=Path)
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--bundle", required=True, type=Path)
    parser.add_argument("--fragments", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    return parser.parse_args()


def main() -> None:
    args = parse_arguments()
    lock = load_json(args.spec_lock)
    if lock.get("schema") != "clangup.build-lock/v1":
        fail("unsupported locked spec schema")
    release = lock["release"]
    required = {target["triple"] for target in lock["targets"] if target["required"]}
    fragment_paths = sorted(args.fragments.rglob("release-fragment.json"))
    if not fragment_paths:
        fail("no release fragments found")

    output = args.output.resolve()
    shutil.rmtree(output, ignore_errors=True)
    output.mkdir(parents=True)
    artifacts: list[dict[str, str]] = []
    seen: set[str] = set()
    for fragment_path in fragment_paths:
        fragment = load_json(fragment_path)
        if fragment.get("schema") != "clangup.release-fragment/v1":
            fail(f"unsupported fragment schema in {fragment_path}")
        if fragment.get("release") != release:
            fail(f"release identity mismatch in {fragment_path}")
        triple = fragment["target"]
        if triple in seen:
            fail(f"duplicate release fragment for {triple}")
        seen.add(triple)
        if triple not in {target["triple"] for target in lock["targets"]}:
            fail(f"fragment target is absent from locked spec: {triple}")

        directory = fragment_path.parent
        artifact_source = safe_child(directory, fragment["artifact"])
        manifest_source = safe_child(directory, fragment["manifest"])
        record_source = safe_child(directory, fragment["build_record"])
        manifest = load_json(manifest_source)
        if (
            manifest.get("schema") != "clangup.artifact/v1"
            or manifest.get("release") != release
        ):
            fail(f"invalid artifact manifest for {triple}")
        runtime = manifest.get("runtime_requirements", {})
        if runtime.get("triple") != triple:
            fail(f"manifest triple mismatch for {triple}")
        artifact_digest = copy_verified(
            artifact_source,
            output / "artifacts" / artifact_source.name,
            manifest["artifact"]["sha256"],
        )
        manifest_destination = output / "manifests" / triple / "manifest.json"
        manifest_digest = copy_verified(manifest_source, manifest_destination)
        copy_verified(
            record_source, output / "build-records" / triple / "build-record.json"
        )
        artifacts.append(
            {
                "target": triple,
                "manifest": str(manifest_destination.relative_to(output)),
                "manifest_sha256": manifest_digest,
                "payload": str((Path("artifacts") / artifact_source.name)),
                "payload_sha256": artifact_digest,
            }
        )

    missing = sorted(required - seen)
    if missing:
        fail(f"required target matrix is incomplete: {', '.join(missing)}")
    unexpected = sorted(seen - {target["triple"] for target in lock["targets"]})
    if unexpected:
        fail(f"unexpected target fragments: {', '.join(unexpected)}")

    source = lock["source"]
    source_destination = output / "objects" / "sources" / f"{source['sha256']}.tar.xz"
    copy_verified(args.source.resolve(), source_destination, source["sha256"])
    objects = [
        {
            "kind": "source",
            "path": str(source_destination.relative_to(output)),
            "sha256": source["sha256"],
        }
    ]
    bundle_root = args.bundle.resolve()
    for patch in source["patches"]:
        relative = Path(patch["path"])
        if relative.is_absolute() or ".." in relative.parts:
            fail(f"unsafe patch path {patch['path']!r}")
        patch_source = bundle_root / relative
        if patch_source.is_symlink() or not patch_source.is_file():
            fail(f"patch is missing or unsafe: {patch_source}")
        destination = output / "objects" / "patches" / f"{patch['sha256']}.patch"
        copy_verified(patch_source, destination, patch["sha256"])
        objects.append(
            {
                "kind": "patch",
                "path": str(destination.relative_to(output)),
                "sha256": patch["sha256"],
            }
        )

    lock_destination = output / "spec.lock.json"
    copy_verified(args.spec_lock.resolve(), lock_destination)
    descriptor = {
        "schema": "clangup.release-bundle/v1",
        "channel": release["channel"],
        "version": release["version"],
        "release": release["release"],
        "locked_spec": "spec.lock.json",
        "locked_spec_sha256": sha256_file(lock_destination),
        "artifacts": sorted(artifacts, key=lambda item: item["target"]),
        "objects": objects,
    }
    (output / "bundle.json").write_bytes(canonical_json(descriptor) + b"\n")
    print(f"assembled {output / 'bundle.json'} with {len(artifacts)} artifacts")


if __name__ == "__main__":
    main()

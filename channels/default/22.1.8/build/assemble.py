#!/usr/bin/env python3
"""Assemble target outputs into one default-channel release."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import shutil


def load(path: Path) -> dict:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise SystemExit(f"{path}: expected a JSON object")
    return value


def identity(path: Path) -> tuple[int, str]:
    digest = hashlib.sha256()
    with path.open("rb") as file:
        for chunk in iter(lambda: file.read(1024 * 1024), b""):
            digest.update(chunk)
    return path.stat().st_size, digest.hexdigest()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--targets", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    output = args.output.resolve()
    shutil.rmtree(output, ignore_errors=True)
    output.mkdir(parents=True)
    release_identity = None
    artifacts = []
    for descriptor_path in sorted(args.targets.rglob("target.json")):
        descriptor = load(descriptor_path)
        if descriptor.get("schema") != "clangup.channel-target/v1":
            raise SystemExit(f"unsupported target descriptor: {descriptor_path}")
        if release_identity is None:
            release_identity = descriptor["release"]
        elif release_identity != descriptor["release"]:
            raise SystemExit("target release identities differ")
        triple = descriptor["target"]
        source_dir = descriptor_path.parent
        destination = output / "targets" / triple
        destination.mkdir(parents=True)
        artifact_source = source_dir / descriptor["artifact"]
        manifest_source = source_dir / descriptor["manifest"]
        artifact_destination = destination / "toolchain.tar.zst"
        manifest_destination = destination / "manifest.json"
        shutil.copyfile(artifact_source, artifact_destination)
        shutil.copyfile(manifest_source, manifest_destination)
        artifact_size, artifact_sha = identity(artifact_destination)
        manifest_size, manifest_sha = identity(manifest_destination)
        manifest = load(manifest_destination)
        if manifest.get("artifact", {}).get("sha256") != artifact_sha:
            raise SystemExit(f"artifact identity differs for {triple}")
        prefix = f"releases/{release_identity['channel']}/{release_identity['version']}-{release_identity['release']}/targets/{triple}"
        artifacts.append({
            "target": triple,
            "artifact": {"key": f"{prefix}/toolchain.tar.zst", "size": artifact_size, "sha256": artifact_sha},
            "manifest": {"key": f"{prefix}/manifest.json", "size": manifest_size, "sha256": manifest_sha},
        })
    if release_identity is None:
        raise SystemExit("no target outputs found")
    release = {"schema": "clangup.release/v1", "release": release_identity, "artifacts": artifacts}
    (output / "release.json").write_text(json.dumps(release, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()

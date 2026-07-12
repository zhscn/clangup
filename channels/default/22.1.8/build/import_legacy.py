#!/usr/bin/env python3
"""Convert the existing default release bundle into the channel layout."""

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


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as file:
        for chunk in iter(lambda: file.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verified(root: Path, relative: str, expected: str) -> Path:
    path = root / relative
    if not path.is_file() or sha256(path) != expected:
        raise SystemExit(f"legacy bundle object differs: {relative}")
    return path


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--bundle", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    descriptors = [path for path in args.bundle.rglob("bundle.json") if load(path).get("schema") == "clangup.release-bundle/v1"]
    if len(descriptors) != 1:
        raise SystemExit("expected one clangup.release-bundle/v1 descriptor")
    descriptor = load(descriptors[0])
    root = descriptors[0].parent
    identity = {name: descriptor[name] for name in ("channel", "version", "release")}
    exact = f"{identity['version']}-{identity['release']}"
    output = args.output.resolve()
    shutil.rmtree(output, ignore_errors=True)
    artifacts = []
    for item in descriptor["artifacts"]:
        triple = item["target"]
        destination = output / "targets" / triple
        destination.mkdir(parents=True)
        artifact = destination / "toolchain.tar.zst"
        shutil.copyfile(verified(root, item["payload"], item["payload_sha256"]), artifact)
        manifest = load(verified(root, item["manifest"], item["manifest_sha256"]))
        record_path = root / "build-records" / triple / "build-record.json"
        if not record_path.is_file():
            raise SystemExit(f"legacy build record is missing for {triple}")
        record = load(record_path)
        manifest["artifact"]["name"] = artifact.name
        manifest["optimization"] = {"pgo": False, "bolt": False}
        manifest["build"] = {
            "commit": record["build_commit"],
            "bootstrap": record["bootstrap"],
            "plan_sha256": record["locked_spec_sha256"],
            "source_identity_sha256": record["source_identity_sha256"],
            "host": record["host"],
            "cmake_arguments": record["cmake_arguments"],
            "resources": record["resources"],
            "started_at": record["started_at"],
            "finished_at": record["finished_at"],
        }
        manifest_path = destination / "manifest.json"
        manifest_path.write_text(json.dumps(manifest, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")
        prefix = f"releases/{identity['channel']}/{exact}/targets/{triple}"
        artifacts.append({
            "target": triple,
            "artifact": {"key": f"{prefix}/toolchain.tar.zst", "size": artifact.stat().st_size, "sha256": sha256(artifact)},
            "manifest": {"key": f"{prefix}/manifest.json", "size": manifest_path.stat().st_size, "sha256": sha256(manifest_path)},
        })
    output.mkdir(parents=True, exist_ok=True)
    release = {"schema": "clangup.release/v1", "release": identity, "artifacts": sorted(artifacts, key=lambda item: item["target"])}
    (output / "release.json").write_text(json.dumps(release, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()

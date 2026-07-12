#!/usr/bin/env python3
"""Stage a legacy default-channel bundle in the current repository layout."""

from __future__ import annotations

import argparse
import json
from pathlib import Path

import stage


def locate(root: Path, entry: dict) -> Path:
    if "path" in entry:
        path = root / entry["path"]
        if path.is_file() and stage.sha256_file(path) == entry["sha256"]:
            return path
    candidates = [path for path in root.rglob(Path(entry["key"]).name) if path.is_file()]
    for path in candidates:
        if path.stat().st_size == entry["size"] and stage.sha256_file(path) == entry["sha256"]:
            return path
    stage.fail(f"bundle object is missing: {entry['key']}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--endpoint", required=True)
    parser.add_argument("--token", required=True)
    parser.add_argument("--bundle", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    descriptors = [path for path in args.bundle.rglob("bundle.json") if stage.load_json(path).get("schema") == "clangup.release-bundle/v1"]
    if len(descriptors) != 1:
        stage.fail("expected exactly one legacy bundle.json")
    old = stage.load_json(descriptors[0])
    root = descriptors[0].parent
    identity = {key: old[key] for key in ("channel", "version", "release")}
    exact = f"{identity['version']}-{identity['release']}"
    prefix = f"releases/{identity['channel']}/{exact}"

    def convert(entry: dict, key: str, content_type: str) -> dict:
        item = stage.object_entry(locate(root, entry), key, content_type)
        if item["sha256"] != entry["sha256"]:
            stage.fail(f"bundle digest differs: {entry['key']}")
        return item

    source_entry = next(item for item in old["objects"] if item["kind"] == "source")
    source = convert(source_entry, f"sources/llvm/{identity['version']}/llvm-project.tar.xz", "application/x-xz")
    locked_entry = {"path": old["locked_spec"], "sha256": old["locked_spec_sha256"]}
    locked = convert(locked_entry, f"{prefix}/inputs/spec.lock.json", "application/json")
    lock = stage.load_json(Path(locked["path"]))
    patch_entries = [item for item in old["objects"] if item["kind"] == "patch"]
    patches = [convert(item, f"{prefix}/inputs/patches/{Path(lock_patch['path']).name}", "text/x-patch") for item, lock_patch in zip(patch_entries, lock["source"]["patches"], strict=True)]
    stage.upload(args.endpoint, args.token, [source, locked, *patches])
    artifacts = []
    for target in old["artifacts"]:
        target_prefix = f"{prefix}/targets/{target['target']}"
        artifact = convert({"path": target["payload"], "sha256": target["payload_sha256"]}, f"{target_prefix}/toolchain.tar.zst", "application/zstd")
        manifest = convert({"path": target["manifest"], "sha256": target["manifest_sha256"]}, f"{target_prefix}/manifest.json", "application/json")
        record = stage.load_json(locate(root, {"path": target["build_record"], "sha256": target["build_record_sha256"]}))
        stage.upload(args.endpoint, args.token, [artifact, manifest])
        build = {
            "commit": record["build_commit"],
            "bootstrap": record["bootstrap"],
            "locked_spec_sha256": record["locked_spec_sha256"],
            "source_identity_sha256": record["source_identity_sha256"],
        }
        artifacts.append({"target": target["target"], "artifact": stage.public_entry(artifact), "manifest": stage.public_entry(manifest), "build": build})
    candidate = {
        "schema": "clangup.release-candidate/v1",
        "release": identity,
        "inputs": {
            "locked_spec": stage.public_entry(locked),
            "source": stage.public_entry(source),
            "patches": [stage.public_entry(item) for item in patches],
            "patchset_sha256": lock["source"]["patchset_sha256"],
        },
        "artifacts": sorted(artifacts, key=lambda item: item["target"]),
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(candidate, sort_keys=True, separators=(",", ":")) + "\n")
    publish_args = argparse.Namespace(endpoint=args.endpoint, token=args.token, candidate=args.output)
    print(json.dumps(stage.publish_release(publish_args), sort_keys=True))


if __name__ == "__main__":
    main()

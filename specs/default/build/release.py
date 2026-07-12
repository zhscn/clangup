#!/usr/bin/env python3
"""Assemble a release descriptor from independently staged objects."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


def fail(message: str) -> None:
    raise SystemExit(f"clangup default release: {message}")


def load(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        fail(f"{path}: expected a JSON object")
    return value


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--spec-lock", required=True, type=Path)
    parser.add_argument("--inputs", required=True, type=Path)
    parser.add_argument("--targets", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()

    lock = load(args.spec_lock)
    release = lock["release"]
    inputs = load(args.inputs)
    if (
        inputs.get("schema") != "clangup.staged-inputs/v1"
        or inputs.get("release") != release
    ):
        fail("staged input identity mismatch")
    required = {target["triple"] for target in lock["targets"] if target["required"]}
    targets = []
    seen = set()
    for path in sorted(args.targets.rglob("staged-target.json")):
        target = load(path)
        triple = target.get("target")
        if (
            target.get("schema") != "clangup.staged-target/v1"
            or target.get("release") != release
        ):
            fail(f"staged target identity mismatch: {path}")
        if triple in seen:
            fail(f"duplicate staged target: {triple}")
        seen.add(triple)
        targets.append(
            {
                "target": triple,
                "artifact": target["artifact"],
                "manifest": target["manifest"],
                "build_record": target["build_record"],
            }
        )
    missing = sorted(required - seen)
    if missing:
        fail(f"required target matrix is incomplete: {', '.join(missing)}")
    descriptor = {
        "schema": "clangup.release/v1",
        "release": release,
        "locked_spec": inputs["locked_spec"],
        "source": inputs["source"],
        "patches": inputs["patches"],
        "artifacts": sorted(targets, key=lambda item: item["target"]),
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(descriptor, sort_keys=True, separators=(",", ":")) + "\n",
        encoding="utf-8",
    )


if __name__ == "__main__":
    main()

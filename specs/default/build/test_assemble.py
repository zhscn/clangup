from __future__ import annotations

import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class AssembleTest(unittest.TestCase):
    def test_assembles_verified_fragment(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "source.tar.xz"
            source.write_bytes(b"source")
            source_digest = hashlib.sha256(source.read_bytes()).hexdigest()
            lock = {
                "schema": "clangup.build-lock/v1",
                "release": {"channel": "default", "version": "1.0.0", "release": 1},
                "source": {
                    "url": "https://example.com/source.tar.xz",
                    "sha256": source_digest,
                    "patches": [],
                    "patchset_sha256": hashlib.sha256(b"[]").hexdigest(),
                },
                "targets": [
                    {
                        "triple": "x86_64-unknown-linux-gnu",
                        "required": True,
                    }
                ],
                "changelog": [],
            }
            lock_path = root / "spec.lock.json"
            self.write_json(lock_path, lock)

            fragment_dir = root / "fragments" / "x86_64"
            fragment_dir.mkdir(parents=True)
            artifact = fragment_dir / "artifact.tar.zst"
            artifact.write_bytes(b"artifact")
            artifact_digest = hashlib.sha256(artifact.read_bytes()).hexdigest()
            manifest = {
                "schema": "clangup.artifact/v1",
                "release": lock["release"],
                "artifact": {"sha256": artifact_digest},
                "runtime_requirements": {"triple": "x86_64-unknown-linux-gnu"},
            }
            self.write_json(fragment_dir / "manifest.json", manifest)
            self.write_json(
                fragment_dir / "build-record.json",
                {"schema": "clangup.build-record/v1"},
            )
            self.write_json(
                fragment_dir / "release-fragment.json",
                {
                    "schema": "clangup.release-fragment/v1",
                    "release": lock["release"],
                    "target": "x86_64-unknown-linux-gnu",
                    "artifact": artifact.name,
                    "manifest": "manifest.json",
                    "build_record": "build-record.json",
                },
            )

            output = root / "output"
            subprocess.run(
                [
                    sys.executable,
                    str(Path(__file__).with_name("assemble.py")),
                    "--spec-lock",
                    str(lock_path),
                    "--source",
                    str(source),
                    "--bundle",
                    str(root),
                    "--fragments",
                    str(root / "fragments"),
                    "--output",
                    str(output),
                ],
                check=True,
            )
            descriptor = json.loads(
                (output / "bundle.json").read_text(encoding="utf-8")
            )
            self.assertEqual(descriptor["schema"], "clangup.release-bundle/v1")
            self.assertEqual(
                descriptor["artifacts"][0]["target"], "x86_64-unknown-linux-gnu"
            )
            self.assertEqual(
                descriptor["artifacts"][0]["payload_sha256"], artifact_digest
            )

    @staticmethod
    def write_json(path: Path, value: object) -> None:
        path.write_text(json.dumps(value, separators=(",", ":")), encoding="utf-8")


if __name__ == "__main__":
    unittest.main()

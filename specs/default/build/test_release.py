from __future__ import annotations

import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class ReleaseTest(unittest.TestCase):
    def test_assembles_staged_target_matrix(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            release = {"channel": "default", "version": "22.1.8", "release": 1}
            lock = {
                "schema": "clangup.build-lock/v1",
                "release": release,
                "targets": [{"triple": "x86_64-unknown-linux-gnu", "required": True}],
            }
            inputs = {
                "schema": "clangup.staged-inputs/v1",
                "release": release,
                "locked_spec": self.object("lock"),
                "source": self.object("source"),
                "patches": [],
            }
            target = {
                "schema": "clangup.staged-target/v1",
                "release": release,
                "target": "x86_64-unknown-linux-gnu",
                "artifact": self.object("artifact"),
                "manifest": self.object("manifest"),
                "build_record": self.object("record"),
            }
            self.write(root / "spec.lock.json", lock)
            self.write(root / "staged-inputs.json", inputs)
            self.write(root / "targets/x86_64/staged-target.json", target)
            output = root / "release.json"
            subprocess.run(
                [
                    sys.executable,
                    str(Path(__file__).with_name("release.py")),
                    "--spec-lock",
                    str(root / "spec.lock.json"),
                    "--inputs",
                    str(root / "staged-inputs.json"),
                    "--targets",
                    str(root / "targets"),
                    "--output",
                    str(output),
                ],
                check=True,
            )
            descriptor = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(descriptor["schema"], "clangup.release/v1")
            self.assertEqual(descriptor["artifacts"][0]["target"], target["target"])

    @staticmethod
    def object(name: str) -> dict[str, object]:
        return {"key": f"objects/{name}", "size": 1, "sha256": "0" * 64}

    @staticmethod
    def write(path: Path, value: object) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value), encoding="utf-8")


if __name__ == "__main__":
    unittest.main()

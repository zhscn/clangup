import importlib.util
from pathlib import Path
import tempfile
import unittest


MODULE_PATH = Path(__file__).with_name("run.py")
SPEC = importlib.util.spec_from_file_location("default_channel_run", MODULE_PATH)
RUN = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(RUN)


class ManifestTest(unittest.TestCase):
    def test_source_identity_has_no_repository_object_reference(self):
        with tempfile.TemporaryDirectory() as directory:
            artifact = Path(directory) / "toolchain.tar.zst"
            artifact.write_bytes(b"artifact")
            plan = {
                "release": {"channel": "default", "version": "22.1.8", "release": 1},
                "source": {
                    "url": "https://example.com/llvm.tar.xz",
                    "sha256": "a" * 64,
                    "patchset_sha256": "b" * 64,
                    "patches": [{"path": "patches/fix.patch", "sha256": "c" * 64, "strip": 1}],
                },
            }
            target = {
                "os": "linux",
                "arch": "x86_64",
                "triple": "x86_64-unknown-linux-gnu",
                "driver_requirements": [],
                "driver": {
                    "libc": "system",
                    "cxx_stdlib": "system",
                    "cxx_stdlib_linkage": "system",
                    "linker": "system",
                    "rtlib": "system",
                    "unwindlib": "system",
                },
            }
            manifest = RUN.make_manifest(plan, target, artifact, {})
            self.assertNotIn("target", manifest["source"]["archive"])
            self.assertNotIn("target", manifest["source"]["patches"][0])


if __name__ == "__main__":
    unittest.main()

import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest import mock


MODULE_PATH = Path(__file__).with_name("run.py")
SPEC = importlib.util.spec_from_file_location("channel_build_run", MODULE_PATH)
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

    def test_bolt_validation_returns_only_recorded_inputs(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            prefix = root / "prefix"
            build = root / "build"
            recorded = prefix / "bin" / "clang-22"
            recorded.parent.mkdir(parents=True)
            (build / "bin").mkdir(parents=True)
            recorded.write_bytes(b"ELF")
            (build / "bin" / "llvm-readelf").touch()
            record = {"bolt": {"enabled": True, "inputs": ["bin/clang-22"]}}
            with mock.patch.object(
                RUN.subprocess, "check_output", return_value=".bolt.org.text\n"
            ):
                self.assertEqual(
                    [recorded], RUN.validate_bolt_outputs(prefix, build, record)
                )

    def test_bolt_relocation_cleanup_only_visits_recorded_inputs(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            build = root / "build"
            recorded = root / "prefix" / "bin" / "clang-22"
            recorded.parent.mkdir(parents=True)
            (build / "bin").mkdir(parents=True)
            recorded.write_bytes(b"\x7fELFpayload")
            (build / "bin" / "llvm-readelf").touch()
            (build / "bin" / "llvm-objcopy").touch()
            sections = "  [ 1] .rela.text RELA 0000000000000000\n"
            with (
                mock.patch.object(RUN.subprocess, "check_output", return_value=sections),
                mock.patch.object(RUN, "run") as run,
            ):
                RUN.drop_bolt_relocations([recorded], build)
            run.assert_called_once()
            self.assertEqual(str(recorded), run.call_args.args[0][-1])


if __name__ == "__main__":
    unittest.main()

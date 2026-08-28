#!/usr/bin/env python3

from pathlib import Path
import re
import unittest


ROOT = Path(__file__).parent
CACHE_ENTRY = re.compile(r"\bset\(\s*([A-Z][A-Z0-9_]*)\b", re.MULTILINE)
COMMAND_LINE_CACHE_ENTRY = re.compile(r"(?:^|\s)-D[A-Z][A-Z0-9_]*", re.MULTILINE)


class CMakeConfigTest(unittest.TestCase):
    def test_build_configuration_uses_initial_cache_files(self) -> None:
        paths = list(ROOT.glob("*.cmake")) + list(ROOT.glob("*.sh"))
        for path in paths:
            contents = path.read_text(encoding="utf-8")
            self.assertIsNone(
                COMMAND_LINE_CACHE_ENTRY.search(contents),
                f"{path.name} contains a -D cache entry",
            )

    def test_common_and_linux_cache_entries_do_not_overlap(self) -> None:
        common = self.cache_entries("common.cmake")
        overlap = common & self.cache_entries("linux.cmake")
        self.assertEqual(set(), overlap)

    def test_driver_and_optimization_contract(self) -> None:
        linux = (ROOT / "linux.cmake").read_text(encoding="utf-8")
        final = (ROOT / "final.cmake").read_text(encoding="utf-8")
        instrumented = (ROOT / "instrumented.cmake").read_text(encoding="utf-8")
        for entry in (
            "set(CLANG_DEFAULT_CXX_STDLIB libc++",
            "set(CLANG_DEFAULT_LINKER lld",
            "set(CLANG_DEFAULT_RTLIB compiler-rt",
            "set(CLANG_DEFAULT_UNWINDLIB libgcc",
        ):
            self.assertIn(entry, linux)
        self.assertIn("set(LLVM_BUILD_INSTRUMENTED IR", instrumented)
        self.assertIn("set(LLVM_ENABLE_LTO Thin", final)
        self.assertIn("--emit-relocs", final)

    def cache_entries(self, name: str) -> set[str]:
        return set(CACHE_ENTRY.findall((ROOT / name).read_text(encoding="utf-8")))


if __name__ == "__main__":
    unittest.main()

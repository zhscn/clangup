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

    def test_common_and_platform_cache_entries_do_not_overlap(self) -> None:
        common = self.cache_entries("common.cmake")
        for platform in ("linux.cmake", "macos.cmake"):
            overlap = common & self.cache_entries(platform)
            self.assertEqual(set(), overlap, f"duplicate entries in {platform}")

    def test_linux_cxx_runtimes_use_fat_lto_objects(self) -> None:
        contents = (ROOT / "linux-runtimes.cmake").read_text(encoding="utf-8")
        for entry in (
            "set(LIBCXX_ADDITIONAL_COMPILE_FLAGS",
            "set(LIBCXXABI_ADDITIONAL_COMPILE_FLAGS",
        ):
            self.assertIn(entry, contents)
        self.assertEqual(2, contents.count('"-flto;-ffat-lto-objects"'))

    def cache_entries(self, name: str) -> set[str]:
        return set(CACHE_ENTRY.findall((ROOT / name).read_text(encoding="utf-8")))


if __name__ == "__main__":
    unittest.main()

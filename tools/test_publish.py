#!/usr/bin/env python3
"""Tests for tools/publish.py."""

from __future__ import annotations

import hashlib
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import publish


class ToolPublishTests(unittest.TestCase):
    def test_assemble_release_collects_targets(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for target, body in (
                ("x86_64-unknown-linux-gnu", b"linux-ninja"),
                ("aarch64-apple-darwin", b"macos-ninja"),
            ):
                d = root / target
                d.mkdir()
                (d / "ninja.tar.zst").write_bytes(body)

            release, paths = publish.assemble_release("ninja", "1.13.2", root)
            self.assertEqual(release["schema"], "clangup.tool-release/v1")
            self.assertEqual(release["tool"], "ninja")
            self.assertEqual(release["version"], "1.13.2")
            self.assertEqual(
                [a["target"] for a in release["artifacts"]],
                ["aarch64-apple-darwin", "x86_64-unknown-linux-gnu"],
            )
            linux = release["artifacts"][1]["artifact"]
            self.assertEqual(
                linux["key"],
                "tools/ninja/1.13.2/targets/x86_64-unknown-linux-gnu/ninja.tar.zst",
            )
            self.assertEqual(
                linux["sha256"], hashlib.sha256(b"linux-ninja").hexdigest()
            )
            self.assertIn(linux["key"], paths)

    def test_publish_release_uploads_and_publishes(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            d = root / "x86_64-unknown-linux-gnu"
            d.mkdir()
            (d / "ninja.tar.zst").write_bytes(b"payload")
            release, paths = publish.assemble_release("ninja", "1.13.2", root)

            calls: list[tuple[str, dict]] = []

            def fake_request(url: str, token: str, value: dict) -> dict:
                calls.append((url, value))
                if url.endswith("/v1/tools/uploads:presign"):
                    key = value["objects"][0]["key"]
                    return {
                        "schema": "clangup.tool-upload-presign-response/v1",
                        "objects": [{"key": key, "status": "already_present"}],
                    }
                return {
                    "schema": "clangup.tool-publish-response/v1",
                    "tool": "ninja",
                    "version": "1.13.2",
                }

            with mock.patch.object(publish, "request_json", side_effect=fake_request):
                result = publish.publish_release(
                    "https://upload.example", "tok", release, paths
                )
            self.assertEqual(result["tool"], "ninja")
            self.assertEqual(len(calls), 2)
            self.assertTrue(calls[0][0].endswith("/v1/tools/uploads:presign"))
            self.assertTrue(calls[1][0].endswith("/v1/tools:publish"))


if __name__ == "__main__":
    unittest.main()

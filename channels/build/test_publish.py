import importlib.util
from pathlib import Path
import subprocess
import unittest
from unittest import mock


MODULE_PATH = Path(__file__).with_name("publish.py")
SPEC = importlib.util.spec_from_file_location("channel_publish", MODULE_PATH)
PUBLISH = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(PUBLISH)


class PublishTest(unittest.TestCase):
    def test_upload_presigns_one_object_at_a_time_and_recovers(self):
        descriptor = {
            "key": "releases/libcxx/22.1.8-1/targets/x/toolchain.tar.zst",
            "size": 8,
            "sha256": "a" * 64,
            "content_type": "application/zstd",
            "cache_control": "public, max-age=31536000, immutable",
        }
        upload = {
            "schema": "clangup.upload-presign-response/v1",
            "objects": [
                {
                    "key": descriptor["key"],
                    "status": "upload",
                    "method": "PUT",
                    "url": "https://example.invalid/upload",
                    "headers": {"if-none-match": "*"},
                }
            ],
        }
        present = {
            "schema": "clangup.upload-presign-response/v1",
            "objects": [
                {"key": descriptor["key"], "status": "already_present"}
            ],
        }
        with (
            mock.patch.object(PUBLISH, "request_json", side_effect=[upload, present])
            as request,
            mock.patch.object(
                PUBLISH.subprocess,
                "run",
                return_value=subprocess.CompletedProcess([], 1),
            ) as run,
        ):
            PUBLISH.upload_object(
                "https://uploader.example",
                "token",
                descriptor,
                Path("toolchain.tar.zst"),
            )

        self.assertEqual(request.call_count, 2)
        for call in request.call_args_list:
            self.assertEqual(call.args[2]["objects"], [descriptor])
        run.assert_called_once()


if __name__ == "__main__":
    unittest.main()

from __future__ import annotations

import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest


class UploadHandler(BaseHTTPRequestHandler):
    uploaded: dict[str, bytes] = {}

    def do_POST(self) -> None:
        body = json.loads(self.rfile.read(int(self.headers["content-length"])))
        if self.path == "/v1/uploads:presign":
            objects = [
                {
                    "key": item["key"],
                    "status": "upload",
                    "method": "PUT",
                    "url": f"http://{self.server.server_address[0]}:{self.server.server_address[1]}/upload/{index}",
                    "headers": {
                        "content-length": str(item["size"]),
                        "content-type": item["content_type"],
                    },
                }
                for index, item in enumerate(body["objects"])
            ]
            self.respond({"objects": objects})
            return
        if self.path == "/v1/releases:publish":
            self.respond(
                {
                    "schema": "clangup.release-publish-response/v1",
                    "channel": "default",
                    "exact": "1-1",
                    "descriptor": body["descriptor"],
                    "catalog": {
                        "key": "catalog-v1.json",
                        "size": 1,
                        "sha256": "0" * 64,
                    },
                    "written": True,
                }
            )
            return
        statuses = [
            {"key": item["key"], "status": "present"} for item in body["objects"]
        ]
        self.respond({"objects": statuses})

    def do_PUT(self) -> None:
        self.uploaded[self.path] = self.rfile.read(int(self.headers["content-length"]))
        self.send_response(200)
        self.end_headers()

    def respond(self, value: object) -> None:
        contents = json.dumps(value).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(contents)))
        self.end_headers()
        self.wfile.write(contents)

    def log_message(self, _format: str, *_args: object) -> None:
        pass


class StageTest(unittest.TestCase):
    def test_stages_target_objects(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            artifact = root / "clang.tar.zst"
            artifact.write_bytes(b"artifact")
            digest = hashlib.sha256(artifact.read_bytes()).hexdigest()
            release = {"channel": "default", "version": "1", "release": 1}
            self.write(
                root / "manifest.json",
                {"artifact": {"sha256": digest}},
            )
            self.write(
                root / "build-record.json",
                {"artifact_sha256": digest},
            )
            self.write(
                root / "release-fragment.json",
                {
                    "release": release,
                    "target": "x86_64-unknown-linux-gnu",
                    "artifact": artifact.name,
                    "manifest": "manifest.json",
                    "build_record": "build-record.json",
                },
            )
            server = ThreadingHTTPServer(("127.0.0.1", 0), UploadHandler)
            thread = threading.Thread(target=server.serve_forever)
            thread.start()
            try:
                output = root / "staged-target.json"
                subprocess.run(
                    [
                        sys.executable,
                        str(Path(__file__).with_name("stage.py")),
                        "--endpoint",
                        f"http://127.0.0.1:{server.server_address[1]}",
                        "--token",
                        "test",
                        "--output",
                        str(output),
                        "target",
                        "--fragment",
                        str(root / "release-fragment.json"),
                    ],
                    check=True,
                )
            finally:
                server.shutdown()
                thread.join()
                server.server_close()
            staged = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(staged["schema"], "clangup.staged-target/v1")
            self.assertEqual(staged["artifact"]["sha256"], digest)
            self.assertEqual(len(UploadHandler.uploaded), 3)

    def test_publishes_staged_release(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            descriptor = {
                "key": "releases/default/1-1/release.json",
                "size": 42,
                "sha256": "a" * 64,
            }
            self.write(
                root / "staged-release.json",
                {"schema": "clangup.staged-file/v1", "object": descriptor},
            )
            server = ThreadingHTTPServer(("127.0.0.1", 0), UploadHandler)
            thread = threading.Thread(target=server.serve_forever)
            thread.start()
            try:
                output = root / "published-release.json"
                subprocess.run(
                    [
                        sys.executable,
                        str(Path(__file__).with_name("stage.py")),
                        "--endpoint",
                        f"http://127.0.0.1:{server.server_address[1]}",
                        "--token",
                        "test",
                        "--output",
                        str(output),
                        "publish",
                        "--descriptor",
                        str(root / "staged-release.json"),
                    ],
                    check=True,
                )
            finally:
                server.shutdown()
                thread.join()
                server.server_close()
            result = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(result["schema"], "clangup.release-publish-response/v1")
            self.assertEqual(result["descriptor"], descriptor)

    @staticmethod
    def write(path: Path, value: object) -> None:
        path.write_text(json.dumps(value), encoding="utf-8")


if __name__ == "__main__":
    unittest.main()

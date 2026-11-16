"""Regression checks for archive identity and publication refusal paths."""

import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest

from release_artifacts import checksums, image_config_digest

IMAGE = "ghcr.io/madhifallah/guarded-batch-operator:0.1.0"
REVISION = "1" * 40
SCRIPTS = Path(__file__).resolve().parent


class ImageIdentityTests(unittest.TestCase):
    def test_config_identity_is_independent_of_docker_archive_layout(self):
        config = b'{"architecture":"amd64","os":"linux"}'
        digest = hashlib.sha256(config).hexdigest()
        for config_name in [digest + ".json", "blobs/sha256/" + digest]:
            with self.subTest(config_name=config_name), tempfile.TemporaryDirectory() as work:
                path = Path(work) / "image.tar.gz"
                self.write_archive(path, config_name, config, [IMAGE])
                self.assertEqual(image_config_digest(path, IMAGE), "sha256:" + digest)
                with self.assertRaises(ValueError):
                    image_config_digest(path, IMAGE + "-other")

    def test_multiple_images_are_refused(self):
        with tempfile.TemporaryDirectory() as work:
            path = Path(work) / "image.tar.gz"
            self.write_archive(path, "config.json", b"{}", [IMAGE], copies=2)
            with self.assertRaises(ValueError):
                image_config_digest(path, IMAGE)

    def test_config_content_determines_identity(self):
        with tempfile.TemporaryDirectory() as work:
            path = Path(work) / "image.tar.gz"
            self.write_archive(path, "config.json", b"{}", [IMAGE])
            before = image_config_digest(path, IMAGE)
            self.write_archive(path, "config.json", b'{"changed":true}', [IMAGE])
            self.assertNotEqual(image_config_digest(path, IMAGE), before)

    @staticmethod
    def write_archive(path, config_name, config, tags, copies=1):
        manifest = json.dumps([{"Config": config_name, "RepoTags": tags}] * copies).encode()
        with tarfile.open(path, "w:gz") as archive:
            for name, data in [("manifest.json", manifest), (config_name, config)]:
                item = tarfile.TarInfo(name)
                item.size = len(data)
                archive.addfile(item, io.BytesIO(data))


class PublisherGuardTests(unittest.TestCase):
    def test_release_lookup_failure_never_reaches_docker(self):
        for failure in ["HTTP 403: forbidden", "HTTP 503: unavailable"]:
            with self.subTest(failure=failure):
                result, calls = self.run_publisher(lookup_error=failure)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(failure, result.stderr)
                self.assertNotIn("docker", calls)

    def test_existing_release_including_draft_is_refused(self):
        result, calls = self.run_publisher(tags="v0.0.9\nv0.1.0\nv0.2.0\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Release already exists", result.stderr)
        self.assertNotIn("docker", calls)

    def test_existing_release_in_large_paginated_result_is_refused(self):
        tags = "v0.1.0\n" + "".join(f"v1.0.{i}\n" for i in range(6000))
        result, calls = self.run_publisher(tags=tags)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Release already exists", result.stderr)
        self.assertNotIn("docker", calls)

    def test_mismatched_candidate_is_refused_after_successful_lookup(self):
        result, calls = self.run_publisher(tags="v0.0.9\n", mismatched_candidate=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Release metadata does not match", result.stderr)
        self.assertNotIn("docker", calls)

    def run_publisher(self, tags="", lookup_error="", mismatched_candidate=False):
        with tempfile.TemporaryDirectory(prefix="guarded-publisher-test-") as work:
            root = Path(work)
            (root / "scripts").mkdir()
            (root / "stubs").mkdir()
            shutil.copyfile(SCRIPTS / "publish-release.sh", root / "scripts/publish-release.sh")
            (root / "VERSION").write_text("0.1.0\n")
            for name, source in {
                "git": """#!/usr/bin/env python3
import os, sys
if sys.argv[1] in ('rev-parse', 'ls-remote'):
    print(os.environ['RELEASE_REVISION'])
""",
                "gh": """#!/usr/bin/env python3
import os, sys
expected = ['api', '--paginate', 'repos/madhifallah/guarded-batch-operator/releases?per_page=100', '--jq', '.[].tag_name']
if sys.argv[1:] != expected:
    raise SystemExit('Unexpected GitHub command')
if os.environ['LOOKUP_ERROR']:
    print(os.environ['LOOKUP_ERROR'], file=sys.stderr)
    raise SystemExit(1)
print(os.environ['RELEASE_TAGS'])
""",
                "docker": """#!/usr/bin/env python3
import os
from pathlib import Path
Path(os.environ['COMMAND_LOG']).write_text('docker')
raise SystemExit('Docker must not run on a refused publication')
""",
            }.items():
                stub = root / "stubs" / name
                stub.write_text(source)
                stub.chmod(0o755)
            if mismatched_candidate:
                out = root / "dist/release"
                out.mkdir(parents=True)
                (out / "release.json").write_text(json.dumps({
                    "tag": "v0.1.0", "revision": REVISION, "version": "0.2.0",
                    "repository": "madhifallah/guarded-batch-operator",
                    "platform": "linux/amd64", "image": IMAGE,
                }))
                checksums(out)
            log = root / "commands.log"
            result = subprocess.run(["bash", str(root / "scripts/publish-release.sh")],
                                    env={**os.environ, "PATH": str(root / "stubs") + ":" + os.environ["PATH"],
                                         "RELEASE_TAG": "v0.1.0", "RELEASE_REVISION": REVISION,
                                         "GITHUB_REPOSITORY": "madhifallah/guarded-batch-operator",
                                         "RELEASE_TAGS": tags, "LOOKUP_ERROR": lookup_error,
                                         "COMMAND_LOG": str(log)},
                                    capture_output=True, text=True, timeout=15)
            return result, log.read_text() if log.exists() else ""


if __name__ == "__main__":
    unittest.main()

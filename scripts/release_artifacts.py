"""Portable Docker archive identity and release checksums (standard library only)."""

import hashlib
import json
from pathlib import Path
import sys
import tarfile


def sha256(content):
    digest = hashlib.sha256()
    for chunk in iter(lambda: content.read(1024 * 1024), b""):
        digest.update(chunk)
    return digest.hexdigest()


def image_config_digest(archive_path, image):
    # Docker's inspect Id varies with the image store. The saved config bytes do not.
    with tarfile.open(archive_path, "r:*") as archive:
        manifest = json.load(archive.extractfile("manifest.json"))
        if len(manifest) != 1 or manifest[0].get("RepoTags") != [image]:
            raise ValueError("Expected exactly one image with the qualified release tag")
        config = archive.extractfile(manifest[0]["Config"])
        if config is None:
            raise ValueError("Image configuration is missing")
        return "sha256:" + sha256(config)


def checksums(directory):
    lines = []
    for path in sorted(directory.iterdir()):
        if path.name != "SHA256SUMS":
            with path.open("rb") as content:
                lines.append(f"{sha256(content)}  {path.name}\n")
    (directory / "SHA256SUMS").write_text("".join(lines))


if __name__ == "__main__":
    release = json.loads(Path(sys.argv[2]).read_text())
    if image_config_digest(sys.argv[1], release["image"]) != release["image_id"]:
        raise ValueError("Loaded image configuration differs from the qualified candidate")

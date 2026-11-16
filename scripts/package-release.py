#!/usr/bin/env python3
"""Package the compiled controller and a non-root image for isolated qualification."""

import gzip
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "dist/release"


def command(*args, **kwargs):
    return subprocess.run(args, cwd=ROOT, check=True, **kwargs)


def checksums(directory):
    files = sorted(p for p in directory.iterdir() if p.name != "SHA256SUMS")
    (directory / "SHA256SUMS").write_text("".join(
        f"{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n" for p in files
    ))


def main():
    version = (ROOT / "VERSION").read_text().strip()
    if not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", version):
        raise ValueError("VERSION must be a stable semantic version without a v prefix")
    revision = command("git", "rev-parse", "HEAD", capture_output=True, text=True).stdout.strip()
    binary = ROOT / "bin/manager"
    header = binary.read_bytes()[:20]
    if header[:6] != b"\x7fELF\x02\x01" or header[18:20] != b"\x3e\x00":
        raise ValueError("Release binary must be Linux AMD64 ELF")
    if OUT.exists():
        raise ValueError("dist/release already exists; remove that owned output before packaging")
    OUT.mkdir(parents=True)
    repository = "madhifallah/guarded-batch-operator"
    image = f"ghcr.io/{repository}:{version}"
    stem = f"guarded-batch-operator_{version}"
    binary_archive = f"{stem}_linux_amd64.tar.gz"
    image_archive = f"{stem}_image_linux_amd64.tar.gz"
    with tempfile.TemporaryDirectory(prefix="guarded-release-") as work:
        context = Path(work)
        shutil.copyfile(binary, context / "manager")
        (context / "manager").chmod(0o755)
        (context / "Dockerfile").write_text(
            'FROM scratch\nCOPY manager /manager\nUSER 65532:65532\nENTRYPOINT ["/manager"]\n'
        )
        command("docker", "build", "--platform", "linux/amd64", "--tag", image,
                "--label", f"org.opencontainers.image.source=https://github.com/{repository}",
                "--label", f"org.opencontainers.image.revision={revision}",
                "--label", f"org.opencontainers.image.version={version}",
                "--label", "org.opencontainers.image.licenses=Apache-2.0", str(context))
        info = json.loads(command("docker", "image", "inspect", image, capture_output=True, text=True).stdout)[0]
        if (info["Architecture"], info["Os"], info["Config"]["User"]) != ("amd64", "linux", "65532:65532"):
            raise ValueError("Unexpected release image platform or user")
        smoke = command("docker", "run", "--rm", "--network", "none", image, "--help",
                        capture_output=True, text=True, timeout=30)
        if "-namespace" not in smoke.stdout + smoke.stderr:
            raise ValueError("Controller CLI smoke check failed")
        with (OUT / binary_archive).open("wb") as raw:
            with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0) as compressed:
                with tarfile.open(fileobj=compressed, mode="w") as archive:
                    for source, name in [(binary, "guarded-batch-operator"), (ROOT / "LICENSE", "LICENSE")]:
                        item = archive.gettarinfo(str(source), arcname=name)
                        item.uid = item.gid = item.mtime = 0
                        item.uname = item.gname = ""
                        with source.open("rb") as content:
                            archive.addfile(item, content)
        saved = context / "image.tar"
        command("docker", "image", "save", "--output", str(saved), image)
        with saved.open("rb") as source, (OUT / image_archive).open("wb") as raw:
            with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0) as compressed:
                shutil.copyfileobj(source, compressed)
    manifest = command(str(ROOT / "bin/kustomize"), "build", "config/default",
                       capture_output=True, text=True).stdout
    if manifest.count("image: controller:latest") != 1:
        raise ValueError("Expected exactly one controller image placeholder")
    (OUT / "install.yaml").write_text(manifest.replace("image: controller:latest", f"image: {image}"))
    (OUT / "release.json").write_text(json.dumps({
        "version": version, "tag": f"v{version}", "revision": revision,
        "repository": repository, "platform": "linux/amd64", "image": image,
        "image_id": info["Id"], "binary_archive": binary_archive, "image_archive": image_archive,
    }, indent=2) + "\n")
    checksums(OUT)
    print(f"Release candidate packaged in {OUT}")


if __name__ == "__main__":
    main()

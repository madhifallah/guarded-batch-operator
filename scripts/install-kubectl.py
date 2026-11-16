#!/usr/bin/env python3
"""Install the pinned Linux kubectl used by disposable-cluster tests."""
import hashlib
import os
from pathlib import Path
import platform
import sys
import tempfile
import urllib.request

VERSION = 'v1.37.0'
PLATFORMS = {
    ('Linux', 'x86_64'): ('amd64', '6129359f4e1f3848a5572ccb0b26cf28b8ca08cef38c95a765b2f64a2c961a2f'),
    ('Linux', 'aarch64'): ('arm64', '922df28df248cc00a9e025f947704f1d1482de64ece54cfe57e61f19eaf1eef3'),
}


def main():
    arch, expected = PLATFORMS[(platform.system(), platform.machine())]
    target = Path(sys.argv[1])
    target.parent.mkdir(parents=True, exist_ok=True)
    url = f'https://dl.k8s.io/release/{VERSION}/bin/linux/{arch}/kubectl'
    with urllib.request.urlopen(url, timeout=60) as response:
        data = response.read()
    if hashlib.sha256(data).hexdigest() != expected:
        raise RuntimeError('kubectl checksum mismatch')
    with tempfile.NamedTemporaryFile(dir=target.parent, delete=False) as output:
        temporary = Path(output.name)
        try:
            output.write(data)
            output.flush()
            temporary.chmod(0o755)
            os.replace(temporary, target)
        finally:
            temporary.unlink(missing_ok=True)
    print(f'Installed checksum-verified kubectl {VERSION} ({arch})')


if __name__ == '__main__':
    main()

#!/usr/bin/env python3
"""Fetch and extract checksum-pinned Sota cores for both supported architectures."""

import argparse
import hashlib
import io
import json
import pathlib
import tarfile
import urllib.request


ROOT = pathlib.Path(__file__).resolve().parents[1]


def extract(package):
    if package[:8] != b"!<arch>\n":
        raise ValueError("Not a Debian ar archive")
    position = 8
    for _ in range(32):
        if position >= len(package):
            break
        header = package[position : position + 60]
        if len(header) != 60 or header[58:60] != b"`\n":
            raise ValueError("Invalid ar header")
        size = int(header[48:58])
        name = header[:16].decode().strip().rstrip("/")
        content = package[position + 60 : position + 60 + size]
        if len(content) != size:
            raise ValueError("Truncated archive")
        position += 60 + size + size % 2
        if name.startswith("data.tar"):
            with tarfile.open(fileobj=io.BytesIO(content), mode="r:*") as archive:
                member = archive.getmember("./usr/libexec/sota-connect/kittend")
                if not member.isfile():
                    raise ValueError("Core is not a regular file")
                return archive.extractfile(member).read()
    raise ValueError("Core executable not found")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--arch", choices=["amd64", "arm64", "all"], default="all")
    args = parser.parse_args()
    lock = json.loads((ROOT / "core.lock.json").read_text())
    for arch, spec in lock.items():
        if args.arch not in ("all", arch):
            continue
        cache = ROOT / "vendor" / "packages" / spec["url"].rsplit("/", 1)[1]
        cache.parent.mkdir(parents=True, exist_ok=True)
        if not cache.exists():
            print(f"Downloading {arch} package...", flush=True)
            with urllib.request.urlopen(spec["url"], timeout=60) as response:
                package = response.read(100 * 1024 * 1024 + 1)
            if len(package) > 100 * 1024 * 1024:
                raise ValueError("Package exceeds 100 MiB")
        else:
            package = cache.read_bytes()
        if hashlib.sha256(package).hexdigest() != spec["package_sha256"]:
            raise ValueError(f"{arch} package checksum mismatch; upstream latest may have changed")
        core = extract(package)
        if hashlib.sha256(core).hexdigest() != spec["core_sha256"]:
            raise ValueError(f"{arch} core checksum mismatch")
        if core[:4] != b"\x7fELF" or int.from_bytes(core[18:20], "little") != spec["elf_machine"]:
            raise ValueError(f"{arch} ELF architecture mismatch")
        cache.write_bytes(package)
        output = ROOT / "vendor" / "kittend" / arch / "kittend"
        output.parent.mkdir(parents=True, exist_ok=True)
        temporary = output.with_suffix(".tmp")
        temporary.write_bytes(core)
        temporary.chmod(0o755)
        temporary.replace(output)
        print(f"Verified {arch} core from Sota {spec['package_version']}", flush=True)


if __name__ == "__main__":
    main()

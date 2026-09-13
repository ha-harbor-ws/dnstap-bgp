#!/usr/bin/env python3
"""Сборка FreeBSD .pkg для OPNsense 26.7 (ABI FreeBSD:15)."""

from __future__ import annotations

import argparse
import hashlib
import io
import json
import os
import subprocess
import sys
import tarfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
OPNSENSE = Path(__file__).resolve().parent

PKG_NAME = "os-dnstap-bgp"
ORIGIN = "opnsense/os-dnstap-bgp"
COMMENT = "Announce domains captured from DNSTap over BGP"
MAINTAINER = "Igor Novgorodov <igor@novg.net>"
WWW = "https://github.com/ha-harbor-ws/dnstap-bgp"
LICENSE = "MPL20"
PREFIX = "/usr/local"
OPNSENSE_ABI = "26.7"

ARCH_MAP = {
    "amd64": ("amd64", "freebsd:15:x86:64"),
    "arm64": ("aarch64", "freebsd:15:aarch64"),
}


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def go_binary() -> str:
    env_go = os.environ.get("GO")
    if env_go:
        return env_go
    sdk = Path.home() / "sdk" / "go1.27.1" / "bin" / "go.exe"
    if sdk.exists():
        return str(sdk)
    return "go"


def read_version() -> str:
    return (ROOT / "VERSION").read_text(encoding="utf-8").strip()


def read_descr() -> str:
    return (OPNSENSE / "pkg-descr").read_text(encoding="utf-8").strip() + "\n"


def build_freebsd_binary(goarch: str, version: str, out: Path) -> None:
    env = os.environ.copy()
    env["GOOS"] = "freebsd"
    env["GOARCH"] = goarch
    env["CGO_ENABLED"] = "0"
    cmd = [
        go_binary(),
        "build",
        "-trimpath",
        "-ldflags",
        f"-s -w -X main.version={version}",
        "-o",
        str(out),
        ".",
    ]
    print("==> go build", " ".join(cmd[1:]))
    subprocess.run(cmd, cwd=ROOT, env=env, check=True)


def stage_files(stage: Path, binary: Path) -> dict[str, Path]:
    mapping: dict[str, Path] = {}

    def put(src: Path, dest: str, text: bool = False) -> None:
        target = stage / dest.lstrip("/")
        target.parent.mkdir(parents=True, exist_ok=True)
        data = src.read_bytes()
        if text:
            data = data.replace(b"\r\n", b"\n").replace(b"\r", b"\n")
        target.write_bytes(data)
        mapping[dest] = target

    put(binary, f"{PREFIX}/sbin/dnstap-bgp")
    put(OPNSENSE / "rc.d" / "dnstap_bgp", f"{PREFIX}/etc/rc.d/dnstap_bgp", text=True)
    put(
        OPNSENSE / "dnstap-bgp.conf.sample",
        f"{PREFIX}/etc/dnstap-bgp/dnstap-bgp.conf.sample",
        text=True,
    )
    put(
        OPNSENSE / "domains.txt.sample",
        f"{PREFIX}/etc/dnstap-bgp/domains.txt.sample",
        text=True,
    )
    put(
        OPNSENSE / "rc.conf.d" / "dnstap_bgp.sample",
        f"{PREFIX}/etc/rc.conf.d/dnstap_bgp.sample",
        text=True,
    )
    return mapping


def abs_install_path(dest: str) -> str:
    """Путь установки с одним ведущим '/'.

    FreeBSD pkg вызывает pkg_absolutepath(..., fromroot=true). Для tar-имени
    без ведущего '/' он сначала пишет '/', затем снова добавляет '/' перед
    каждым компонентом — получается '//usr/local/...' и ошибка
    «File ... not specified in the manifest».
    """
    name = dest.replace("\\", "/")
    while name.startswith("./"):
        name = name[2:]
    return "/" + name.lstrip("/")


def file_mode(dest: str) -> int:
    if dest.endswith("/dnstap-bgp") and "/sbin/" in dest:
        return 0o755
    if "/rc.d/" in dest:
        return 0o755
    return 0o644


def post_install_script() -> str:
    return """#!/bin/sh
mkdir -p /var/run/dnstap-bgp /var/db/dnstap-bgp \\
    /usr/local/etc/dnstap-bgp /usr/local/etc/rc.conf.d
if [ ! -f /usr/local/etc/dnstap-bgp/dnstap-bgp.conf ]; then
    cp /usr/local/etc/dnstap-bgp/dnstap-bgp.conf.sample \\
        /usr/local/etc/dnstap-bgp/dnstap-bgp.conf
fi
if [ ! -f /usr/local/etc/dnstap-bgp/domains.txt ]; then
    cp /usr/local/etc/dnstap-bgp/domains.txt.sample \\
        /usr/local/etc/dnstap-bgp/domains.txt
fi
if [ ! -f /usr/local/etc/rc.conf.d/dnstap_bgp ]; then
    cp /usr/local/etc/rc.conf.d/dnstap_bgp.sample \\
        /usr/local/etc/rc.conf.d/dnstap_bgp
fi
"""


def pre_deinstall_script() -> str:
    return """#!/bin/sh
if [ -f /var/run/dnstap_bgp.pid ]; then
    /usr/sbin/service dnstap_bgp stop >/dev/null 2>&1 || true
fi
"""


def build_manifest(
    version: str,
    abi: str,
    arch: str,
    product_arch: str,
    files: dict[str, Path],
) -> dict:
    file_meta = {}
    flatsize = 0
    for dest, path in sorted(files.items()):
        digest = sha256_file(path)
        flatsize += path.stat().st_size
        file_meta[abs_install_path(dest)] = f"1${digest}"

    return {
        "name": PKG_NAME,
        "origin": ORIGIN,
        "version": version,
        "comment": COMMENT,
        "maintainer": MAINTAINER,
        "www": WWW,
        "abi": abi,
        "arch": arch,
        "prefix": PREFIX,
        "flatsize": flatsize,
        "licenselogic": "single",
        "licenses": [LICENSE],
        "desc": read_descr(),
        "categories": ["net"],
        "annotations": {
            "product_abi": OPNSENSE_ABI,
            "product_arch": product_arch,
            "product_id": PKG_NAME,
            "product_name": "dnstap-bgp",
            "product_version": version,
            "product_website": WWW,
            "product_email": MAINTAINER,
            "product_tier": "3",
        },
        "files": file_meta,
        "scripts": {
            "post-install": post_install_script(),
            "pre-deinstall": pre_deinstall_script(),
        },
    }


def compact_manifest(manifest: dict) -> dict:
    skip = {"files", "directories", "scripts"}
    return {k: v for k, v in manifest.items() if k not in skip}


def add_text(tar: tarfile.TarFile, name: str, data: bytes, mode: int = 0o644) -> None:
    info = tarfile.TarInfo(name=name)
    info.size = len(data)
    info.mode = mode
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "wheel"
    tar.addfile(info, io.BytesIO(data))


def _set_tarinfo_name(info: tarfile.TarInfo, name: str) -> None:
    info.name = name
    if not info.name.startswith("/") and name.startswith("/"):
        # Python tarfile может срезать ведущий '/'; pkg его требует.
        info.__dict__["_name"] = name


def add_file(tar: tarfile.TarFile, dest: str, path: Path) -> None:
    data = path.read_bytes()
    info = tarfile.TarInfo()
    _set_tarinfo_name(info, abs_install_path(dest))
    info.size = len(data)
    info.mode = file_mode(dest)
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "wheel"
    tar.addfile(info, io.BytesIO(data))


def _ustar_file_size(header: bytes) -> int:
    raw = header[124:136].split(b"\0", 1)[0].strip()
    return int(raw, 8) if raw else 0


def _ustar_checksum(header: bytearray) -> None:
    header[148:156] = b"        "
    chksum = sum(header)
    header[148:156] = f"{chksum:06o}".encode("ascii") + b"\0 "


def ensure_absolute_payload_names(blob: bytes) -> bytes:
    """Гарантирует ведущий '/' у payload-файлов в ustar (не у +MANIFEST)."""
    out = bytearray()
    off = 0
    n = len(blob)
    while off + 512 <= n:
        header = bytearray(blob[off : off + 512])
        if not any(header):
            out.extend(blob[off:])
            break
        name = header[:100].split(b"\0", 1)[0]
        prefix = header[345:500].split(b"\0", 1)[0]
        size = _ustar_file_size(header)
        data_end = off + 512 + ((size + 511) // 512) * 512
        if data_end > n:
            raise ValueError("truncated ustar archive")

        if name.startswith(b"+"):
            out.extend(blob[off:data_end])
        else:
            if prefix:
                raise ValueError(
                    f"ustar prefix split cannot add leading slash: {prefix!r}/{name!r}"
                )
            if not name.startswith(b"/"):
                new_name = b"/" + name
                if len(new_name) > 100:
                    raise ValueError(f"ustar name too long: {new_name!r}")
                header[:100] = new_name + b"\0" * (100 - len(new_name))
                _ustar_checksum(header)
            out.extend(header)
            out.extend(blob[off + 512 : data_end])
        off = data_end
    else:
        if off < n:
            out.extend(blob[off:])
    return bytes(out)


def ustar_member_names(blob: bytes) -> list[str]:
    names: list[str] = []
    off = 0
    n = len(blob)
    while off + 512 <= n:
        header = blob[off : off + 512]
        if not any(header):
            break
        name = header[:100].split(b"\0", 1)[0].decode("utf-8")
        prefix = header[345:500].split(b"\0", 1)[0].decode("utf-8")
        names.append(f"{prefix}/{name}" if prefix else name)
        size = _ustar_file_size(header)
        off += 512 + ((size + 511) // 512) * 512
    return names


def assert_pkg_archive(blob: bytes, files: dict[str, Path]) -> None:
    names = ustar_member_names(blob)
    if len(names) < 2 or names[:2] != ["+COMPACT_MANIFEST", "+MANIFEST"]:
        raise SystemExit(f"pkg metadata members invalid: {names[:2]!r}")
    payload = names[2:]
    expected = [abs_install_path(dest) for dest in sorted(files)]
    if payload != expected:
        raise SystemExit(
            "pkg tar payload names must be absolute and match the manifest:\n"
            f"  got:      {payload!r}\n"
            f"  expected: {expected!r}"
        )


def write_pkg(pkg_path: Path, manifest: dict, files: dict[str, Path]) -> None:
    raw = io.BytesIO()
    with tarfile.open(fileobj=raw, mode="w", format=tarfile.USTAR_FORMAT) as tar:
        man = json.dumps(manifest, ensure_ascii=False, indent=2).encode("utf-8")
        compact = json.dumps(
            compact_manifest(manifest), ensure_ascii=False, indent=2
        ).encode("utf-8")
        add_text(tar, "+COMPACT_MANIFEST", compact)
        add_text(tar, "+MANIFEST", man)
        for dest, path in sorted(files.items()):
            add_file(tar, dest, path)

    payload = ensure_absolute_payload_names(raw.getvalue())
    assert_pkg_archive(payload, files)
    pkg_path.parent.mkdir(parents=True, exist_ok=True)

    try:
        import zstandard

        compressor = zstandard.ZstdCompressor(level=19)
        pkg_path.write_bytes(compressor.compress(payload))
        print("==> compression: zstd")
        return
    except ImportError:
        pass

    import lzma

    pkg_path.write_bytes(
        lzma.compress(payload, format=lzma.FORMAT_XZ, preset=9 | lzma.PRESET_EXTREME)
    )
    print("==> compression: xz (install python-zstandard for tzst)")


def main() -> int:
    parser = argparse.ArgumentParser(description="Build OPNsense 26.7 pkg")
    parser.add_argument("--arch", choices=sorted(ARCH_MAP), default="amd64")
    parser.add_argument("--skip-build", action="store_true")
    args = parser.parse_args()

    goarch = args.arch
    product_arch, pkg_arch = ARCH_MAP[goarch]
    abi = f"FreeBSD:15:{product_arch}"
    version = read_version()

    dist = ROOT / "dist"
    work = ROOT / "work" / "opnsense"
    stage = work / "stage"
    binary = work / "dnstap-bgp"
    work.mkdir(parents=True, exist_ok=True)

    if not args.skip_build:
        build_freebsd_binary(goarch, version, binary)
    elif not binary.exists():
        print(f"missing binary: {binary}", file=sys.stderr)
        return 1

    if stage.exists():
        for p in sorted(stage.rglob("*"), reverse=True):
            if p.is_file() or p.is_symlink():
                p.unlink()
            elif p.is_dir():
                p.rmdir()
    stage.mkdir(parents=True, exist_ok=True)

    files = stage_files(stage, binary)
    manifest = build_manifest(version, abi, pkg_arch, product_arch, files)

    pkg_name = f"{PKG_NAME}-{version}-opnsense{OPNSENSE_ABI}-freebsd15-{product_arch}.pkg"
    pkg_path = dist / pkg_name
    write_pkg(pkg_path, manifest, files)

    print(f"==> {pkg_path}")
    print(f"==> abi {abi} arch {pkg_arch} size {pkg_path.stat().st_size} bytes")
    return 0


if __name__ == "__main__":
    sys.exit(main())

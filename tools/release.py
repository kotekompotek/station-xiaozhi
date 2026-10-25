"""Assemble the station-xiaozhi release: an installer folder, its ZIP, and a yam .yap package."""

import hashlib
import re
import shutil
import sys
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PACKAGE = ROOT / "package"
BINARY = ROOT / "build" / "station-xiaozhi"
DIST = ROOT / "dist"

# Published path -> (source, line endings, executable). Line endings: "lf", "crlf", "bom" (CRLF + UTF-8 BOM
# for Windows PowerShell 5.1, which otherwise reads scripts in the ANSI code page), or None for binary.
LAYOUT = {
    "station-xiaozhi": (BINARY, None, True),
    "station/run.sh": (PACKAGE / "station/run.sh", "lf", True),
    "station/setup.sh": (PACKAGE / "station/setup.sh", "lf", True),
    "station/S99station-xiaozhi": (PACKAGE / "station/S99station-xiaozhi", "lf", True),
    "install.sh": (PACKAGE / "install.sh", "lf", True),
    "install.ps1": (PACKAGE / "install.ps1", "bom", False),
    "install.cmd": (PACKAGE / "install.cmd", "crlf", False),
    "uninstall.cmd": (PACKAGE / "uninstall.cmd", "crlf", False),
    "README.md": (PACKAGE / "README.md", "lf", False),
    "roles/queen.md": (PACKAGE / "roles/queen.md", "lf", False),
    "roles/business-woman.md": (PACKAGE / "roles/business-woman.md", "lf", False),
}


def version() -> str:
    match = re.search(r'const Version = "([^"]+)"', (ROOT / "internal/voice/paths.go").read_text("utf-8"))
    if not match:
        raise SystemExit("Version constant not found in internal/voice/paths.go")
    return match.group(1)


def content(source: Path, endings: str | None) -> bytes:
    data = source.read_bytes()
    if endings is None:
        return data
    text = data.decode("utf-8").lstrip("﻿").replace("\r\n", "\n")
    if endings == "lf":
        return text.encode("utf-8")
    text = text.replace("\n", "\r\n")
    return ("﻿" + text if endings == "bom" else text).encode("utf-8")


def add(archive: zipfile.ZipFile, name: str, data: bytes, executable: bool) -> None:
    info = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
    info.compress_type = zipfile.ZIP_DEFLATED
    info.external_attr = (0o100755 if executable else 0o100644) << 16
    archive.writestr(info, data)


def main() -> None:
    if not BINARY.is_file():
        raise SystemExit("Build the ARM client first: make build")
    v = version()
    name = f"station-xiaozhi-{v}"
    folder = DIST / name
    shutil.rmtree(folder, ignore_errors=True)
    files = {path: (content(src, endings), executable) for path, (src, endings, executable) in LAYOUT.items()}

    for path, (data, _) in files.items():
        target = folder / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
    with zipfile.ZipFile(DIST / f"{name}.zip", "w") as archive:
        for path, (data, executable) in files.items():
            add(archive, f"{name}/{path}", data, executable)

    manifest = content(PACKAGE / "yam/plugin.toml", "lf").replace(b"@VERSION@", v.encode())
    with zipfile.ZipFile(DIST / f"{name}.yap", "w") as archive:
        add(archive, "plugin.toml", manifest, False)
        add(archive, "run.sh", files["station/run.sh"][0], True)
        add(archive, "station-xiaozhi", files["station-xiaozhi"][0], True)

    sums = "".join(
        f"{hashlib.sha256((DIST / artifact).read_bytes()).hexdigest()}  {artifact}\n"
        for artifact in (f"{name}.zip", f"{name}.yap")
    )
    (DIST / "SHA256SUMS").write_text(sums, "utf-8", newline="\n")
    print(f"Release {v}:\n  {folder}\n  {DIST / (name + '.zip')}\n  {DIST / (name + '.yap')}")


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Derive every icon artifact in the repo from a single source image.

Source of truth is `build/appicon.png`. Replace that one file and re-run:

    make icons

Generated, and meant to be committed:

    build/windows/icon.ico      multi-size ICO for the .exe and the NSIS installer
    frontend/public/appicon.png shown in the app header and used as the favicon
    docs/assets/appicon.png     logo + favicon of the MkDocs site

Requires Pillow (`pip install pillow`). Icon generation is a rare maintenance
step, so this is deliberately not wired into `make linux` / `make windows` —
the generated files are committed and the normal build just uses them.
"""

from __future__ import annotations

import sys
from pathlib import Path

try:
    from PIL import Image
except ImportError:
    sys.exit("Thiếu Pillow. Cài bằng: pip install pillow")

REPO = Path(__file__).resolve().parent.parent
SOURCE = REPO / "build" / "appicon.png"

# Sizes Windows actually picks between; 256 is what modern shells use.
ICO_SIZES = [(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)]

# The app header renders it at 34 px and the favicon at 16–32 px, so 256 is
# plenty and keeps the embedded frontend small.
WEB_SIZE = 256


def load_source() -> Image.Image:
    if not SOURCE.exists():
        sys.exit(f"Không tìm thấy {SOURCE.relative_to(REPO)}")

    img = Image.open(SOURCE)
    width, height = img.size

    if width != height:
        sys.exit(
            f"Icon phải là ảnh vuông, hiện tại {width}x{height}. "
            "Cắt lại rồi lưu vào build/appicon.png."
        )
    if width < 256:
        sys.exit(
            f"Icon quá nhỏ ({width}x{height}). Cần tối thiểu 256x256, "
            "nên dùng 1024x1024 để .ico 256px không bị nhoè."
        )
    if width < 1024:
        print(
            f"  ! {width}x{height} — nên dùng 1024x1024 để nét ở mọi kích cỡ",
            file=sys.stderr,
        )

    # RGBA so rounded corners stay transparent instead of turning black.
    return img.convert("RGBA")


def write(path: Path, img: Image.Image, size: int) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    img.resize((size, size), Image.LANCZOS).save(path, "PNG")
    print(f"  → {path.relative_to(REPO)}  ({size}x{size})")


def main() -> None:
    img = load_source()
    print(f"Nguồn: {SOURCE.relative_to(REPO)}  ({img.width}x{img.height})")

    ico = REPO / "build" / "windows" / "icon.ico"
    ico.parent.mkdir(parents=True, exist_ok=True)
    img.save(ico, "ICO", sizes=ICO_SIZES)
    print(f"  → {ico.relative_to(REPO)}  ({len(ICO_SIZES)} kích cỡ)")

    write(REPO / "frontend" / "public" / "appicon.png", img, WEB_SIZE)
    write(REPO / "docs" / "assets" / "appicon.png", img, WEB_SIZE)

    print("Xong. Nhớ commit các file vừa sinh ra.")


if __name__ == "__main__":
    main()

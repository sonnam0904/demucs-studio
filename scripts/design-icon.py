#!/usr/bin/env python3
"""Draw the DemucsStudio app icon and write it to build/appicon.png.

The mark has to say two things at a glance: *audio*, and *split in two*. So it is
a set of waveform bars cut along a horizontal seam — the upper halves in the
app's vocal colour, the lower halves in its accent colour, with a clear gap
between them. Plain equaliser bars would only say "audio"; the seam is what says
"separation", which is the whole point of the app.

This is deliberately separate from `make icons`:

    python3 scripts/design-icon.py    # regenerates the artwork (destructive)
    make icons                        # derives .ico and web copies from it

So dropping your own artwork into build/appicon.png and running `make icons`
never gets overwritten by a build.

Requires Pillow and numpy.
"""

from __future__ import annotations

import sys
from pathlib import Path

try:
    import numpy as np
    from PIL import Image, ImageDraw, ImageFilter
except ImportError:
    sys.exit("Thiếu thư viện. Cài bằng: pip install pillow numpy")

REPO = Path(__file__).resolve().parent.parent
OUT = REPO / "build" / "appicon.png"

SIZE = 1024
# Draw oversized and downscale: Pillow's rounded_rectangle has no antialiasing,
# so supersampling is what keeps the corners and bar caps from looking jagged.
SCALE = 4
R = SIZE * SCALE

# Matches frontend/src/style.css so the icon and the UI share one palette.
TILE_TOP = (36, 44, 82)  # deep indigo, lit from the top-left
TILE_BOTTOM = (13, 16, 32)  # near --bg #0c0e14
VOCAL_LIGHT = (214, 180, 255)
VOCAL_DARK = (150, 100, 250)
ACCENT_LIGHT = (150, 186, 255)
ACCENT_DARK = (64, 118, 255)

# Bar heights as a fraction of the available half-height. Asymmetric on purpose:
# a symmetric run of bars reads as a chart, an uneven one reads as a waveform.
BAR_HEIGHTS = [0.44, 0.80, 1.0, 0.68, 0.40]

# Bars must stay legible at 16 px, where thin bars blur into one blob. Keeping
# the gaps narrower than the bars is what makes them survive the downscale.
GAP_RATIO = 0.62


def vertical_gradient(size: tuple[int, int], top: tuple, bottom: tuple) -> Image.Image:
    """A top-to-bottom linear gradient, built with numpy for speed at 4096px."""
    width, height = size
    ramp = np.linspace(0.0, 1.0, height, dtype=np.float32)[:, None]
    top_arr = np.array(top, dtype=np.float32)
    bottom_arr = np.array(bottom, dtype=np.float32)
    rows = top_arr * (1.0 - ramp) + bottom_arr * ramp
    block = np.repeat(rows[:, None, :], width, axis=1)
    return Image.fromarray(block.astype(np.uint8), "RGB")


def rounded_mask(size: tuple[int, int], radius: int) -> Image.Image:
    mask = Image.new("L", size, 0)
    ImageDraw.Draw(mask).rounded_rectangle([(0, 0), (size[0] - 1, size[1] - 1)],
                                           radius=radius, fill=255)
    return mask


def build() -> Image.Image:
    # --- tile ---------------------------------------------------------------
    # A small inset keeps the rounded corners from touching the canvas edge, so
    # the icon still looks right on a light desktop background.
    inset = int(R * 0.035)
    tile_size = R - inset * 2
    corner = int(tile_size * 0.235)

    canvas = Image.new("RGBA", (R, R), (0, 0, 0, 0))

    tile = vertical_gradient((tile_size, tile_size), TILE_TOP, TILE_BOTTOM)
    tile.putalpha(rounded_mask((tile_size, tile_size), corner))
    canvas.alpha_composite(tile, (inset, inset))

    # A one-pixel-ish top highlight suggests a physical, slightly lit surface
    # instead of a flat rectangle.
    rim = Image.new("RGBA", (R, R), (0, 0, 0, 0))
    ImageDraw.Draw(rim).rounded_rectangle(
        [(inset, inset), (R - inset - 1, R - inset - 1)],
        radius=corner, outline=(255, 255, 255, 34), width=max(2, int(R * 0.004)))
    canvas.alpha_composite(rim)

    # --- bars ---------------------------------------------------------------
    count = len(BAR_HEIGHTS)
    content = tile_size * 0.66          # how much of the tile the bars span
    bar_w = content / (count + (count - 1) * GAP_RATIO)
    gap = bar_w * GAP_RATIO
    seam = tile_size * 0.050            # the split: distance from centre to a bar cap
    half_max = tile_size * 0.315        # tallest bar's reach from the centre
    cap = bar_w * 0.5                   # fully rounded bar caps

    centre = R / 2
    left = centre - content / 2

    top_layer = Image.new("RGBA", (R, R), (0, 0, 0, 0))
    bottom_layer = Image.new("RGBA", (R, R), (0, 0, 0, 0))
    top_draw = ImageDraw.Draw(top_layer)
    bottom_draw = ImageDraw.Draw(bottom_layer)

    for i, weight in enumerate(BAR_HEIGHTS):
        x0 = left + i * (bar_w + gap)
        x1 = x0 + bar_w
        reach = half_max * weight

        # Upper half — vocal.
        top_draw.rounded_rectangle(
            [(x0, centre - seam - reach), (x1, centre - seam)],
            radius=cap, fill=(255, 255, 255, 255))
        # Lower half — instrumental. Slightly shorter so the two sides read as
        # two separate results rather than one mirrored shape.
        bottom_draw.rounded_rectangle(
            [(x0, centre + seam), (x1, centre + seam + reach * 0.88)],
            radius=cap, fill=(255, 255, 255, 255))

    # Tint each half with its own gradient, using the white bars as a mask.
    vocal = vertical_gradient((R, R), VOCAL_LIGHT, VOCAL_DARK).convert("RGBA")
    accent = vertical_gradient((R, R), ACCENT_LIGHT, ACCENT_DARK).convert("RGBA")
    vocal.putalpha(top_layer.getchannel("A"))
    accent.putalpha(bottom_layer.getchannel("A"))

    # A soft glow under the bars so they sit in the tile instead of on top of it.
    glow = Image.new("RGBA", (R, R), (0, 0, 0, 0))
    glow.alpha_composite(vocal)
    glow.alpha_composite(accent)
    glow = glow.filter(ImageFilter.GaussianBlur(R * 0.013))
    glow.putalpha(glow.getchannel("A").point(lambda a: int(a * 0.28)))

    canvas.alpha_composite(glow)
    canvas.alpha_composite(vocal)
    canvas.alpha_composite(accent)

    # Clip everything to the tile so the glow cannot bleed past the corners.
    clip = Image.new("L", (R, R), 0)
    clip.paste(rounded_mask((tile_size, tile_size), corner), (inset, inset))
    canvas.putalpha(Image.composite(canvas.getchannel("A"),
                                    Image.new("L", (R, R), 0), clip))

    return canvas.resize((SIZE, SIZE), Image.LANCZOS)


def main() -> None:
    icon = build()
    OUT.parent.mkdir(parents=True, exist_ok=True)
    icon.save(OUT, "PNG")
    print(f"→ {OUT.relative_to(REPO)}  ({SIZE}x{SIZE})")
    print("Tiếp: make icons")


if __name__ == "__main__":
    main()

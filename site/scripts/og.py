#!/usr/bin/env python3
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1]
PUBLIC = ROOT / "public"
ICON = ROOT.parent / "browser" / "icons" / "icon128.png"
OUT = PUBLIC / "og.png"

W, H = 1200, 630
PAPER = (255, 248, 238)
INK = (42, 26, 16)
MUTED = (90, 62, 44)
OWL = (224, 122, 47)
LINE = (232, 201, 168)

BOLD = ImageFont.truetype("/System/Library/Fonts/HelveticaNeue.ttc", 72, index=1)
REG = ImageFont.truetype("/System/Library/Fonts/HelveticaNeue.ttc", 32, index=0)
SMALL = ImageFont.truetype("/System/Library/Fonts/HelveticaNeue.ttc", 24, index=0)


def wrap(draw: ImageDraw.ImageDraw, text: str, font: ImageFont.FreeTypeFont, width: int) -> str:
    words = text.split()
    lines: list[str] = []
    cur = ""
    for word in words:
        trial = f"{cur} {word}".strip()
        if draw.textlength(trial, font=font) <= width:
            cur = trial
        else:
            if cur:
                lines.append(cur)
            cur = word
    if cur:
        lines.append(cur)
    return "\n".join(lines)


def main() -> None:
    img = Image.new("RGB", (W, H), PAPER)
    draw = ImageDraw.Draw(img)
    draw.rounded_rectangle((48, 48, W - 48, H - 48), 28, outline=LINE, width=2)

    icon = Image.open(ICON).convert("RGBA").resize((168, 168), Image.Resampling.LANCZOS)
    img.paste(icon, (88, 220), icon)

    x = 292
    draw.text((x, 218), "pr-buddy", font=BOLD, fill=INK)
    blurb = wrap(
        draw,
        "Understand-first file order for GitHub pull requests. You still write every comment.",
        REG,
        760,
    )
    draw.multiline_text((x, 318), blurb, font=REG, fill=MUTED, spacing=10)
    draw.text((x, 470), "pr-buddy.anubhav.wtf", font=SMALL, fill=OWL)
    img.save(OUT, "PNG")
    print(OUT)


if __name__ == "__main__":
    main()

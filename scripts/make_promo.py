from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw, ImageFilter, ImageFont


root = Path(__file__).resolve().parents[1]
width, height = 2560, 1760
rows, columns = np.mgrid[0:height, 0:width]
horizontal, vertical = columns / width, rows / height
blend = np.clip(horizontal * 0.65 + vertical * 0.35, 0, 1)[..., None]
background = np.array([239, 222, 192]) * (1 - blend) + np.array([170, 105, 76]) * blend
glow = np.exp(-((horizontal - 0.8) ** 2 + (vertical - 0.15) ** 2) / 0.2)
background += glow[..., None] * np.array([15, 19, 10])
grain = np.random.default_rng(20261113).normal(0, 0.65, (height, width, 1))
canvas = Image.fromarray(np.clip(background + grain, 0, 255).astype(np.uint8)).convert("RGBA")
font_path = "/System/Library/Fonts/Helvetica.ttc"
title_font = ImageFont.truetype(font_path, 78)
caption_font = ImageFont.truetype(font_path, 26)
badge_font = ImageFont.truetype(font_path, 22)
draw = ImageDraw.Draw(canvas)
draw.text((220, 90), "MyGo Emby", font=title_font, fill="#33271f")
draw.text((224, 190), "Your library. A native macOS experience.", font=caption_font, fill="#675044")
draw.rounded_rectangle((2130, 110, 2340, 164), radius=27, fill="#46382d")
draw.text((2171, 124), "0.1.0 BETA", font=badge_font, fill="#fff4e0")
shot = Image.open(root / "docs/assets/home.png").convert("RGBA")
shot = shot.resize((2120, 1358), Image.Resampling.LANCZOS)
mask = Image.new("L", shot.size)
ImageDraw.Draw(mask).rounded_rectangle((0, 0, shot.width - 1, shot.height - 1), radius=30, fill=255)
shot.putalpha(mask)
chrome = ImageDraw.Draw(shot)
scale = shot.width / 1280
for index, color in enumerate(("#ff5f57", "#febc2e", "#28c840")):
    left = round((20 + 20 * index) * scale)
    top = round(20 * scale)
    diameter = round(12 * scale)
    chrome.ellipse((left, top, left + diameter, top + diameter), fill=color)
shadow = Image.new("RGBA", canvas.size)
ImageDraw.Draw(shadow).rounded_rectangle((220, 312, 2340, 1670), radius=36, fill=(38, 20, 12, 95))
canvas = Image.alpha_composite(canvas, shadow.filter(ImageFilter.GaussianBlur(34)))
canvas.alpha_composite(shot, (220, 282))
canvas.convert("RGB").save(root / "docs/assets/promo.png", optimize=True)

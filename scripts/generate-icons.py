#!/usr/bin/env python3
"""Generate brand and native icon assets from brand/icon.svg (macOS)."""
from pathlib import Path
import shutil
import struct
import subprocess
import tempfile
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "brand/icon.svg"
ICONS = ROOT / "desktop/icons"
SVG = "http://www.w3.org/2000/svg"
ET.register_namespace("", SVG)


def render(source, destination, size):
    subprocess.run(["rsvg-convert", "-w", str(size), "-h", str(size), "-o", str(destination), str(source)], check=True)


def main():
    ICONS.mkdir(parents=True, exist_ok=True)
    source = SOURCE.read_text()
    shutil.copyfile(SOURCE, ROOT / "ui/public/favicon.svg")
    mark = ET.fromstring(source)
    mark.remove(mark.find(f"{{{SVG}}}rect"))
    mark.find(f"{{{SVG}}}path").set("stroke", "#173A32")
    mark.find(f"{{{SVG}}}circle").set("fill", "#379C65")
    ET.ElementTree(mark).write(ROOT / "brand/mark.svg", encoding="unicode")
    for name, color in (("logo.svg", "#173A32"), ("logo-dark.svg", "#F5FBF4")):
        logo = ET.fromstring(source)
        logo.set("viewBox", "0 0 440 128")
        text = ET.SubElement(logo, f"{{{SVG}}}text", {
            "x": "150", "y": "84", "fill": color, "font-size": "90",
            "font-family": "Inter, ui-sans-serif, system-ui, Helvetica, Arial, sans-serif",
            "font-weight": "700", "letter-spacing": "-4",
        })
        text.text = "grpvn"
        ET.ElementTree(logo).write(ROOT / "brand" / name, encoding="unicode")
    with tempfile.TemporaryDirectory(prefix="grpvn-icons-") as directory:
        temp = Path(directory)
        sizes = (16, 20, 24, 32, 40, 48, 64, 128, 256, 512, 1024)
        for size in sizes:
            render(SOURCE, temp / f"{size}.png", size)
        shutil.copyfile(temp / "1024.png", ICONS / "app.png")
        shutil.copyfile(temp / "512.png", ICONS / "linux.png")
        tray = ET.fromstring(ET.tostring(mark))
        tray.find(f"{{{SVG}}}path").set("stroke", "#000000")
        tray.find(f"{{{SVG}}}circle").set("fill", "#000000")
        ET.ElementTree(tray).write(temp / "tray.svg", encoding="unicode")
        render(temp / "tray.svg", ICONS / "tray.png", 44)
        images = [(size, (temp / f"{size}.png").read_bytes()) for size in sizes if size <= 256]
        offset = 6 + 16 * len(images)
        entries, payload = [], []
        for size, image in images:
            entries.append(struct.pack("<BBBBHHII", size % 256, size % 256, 0, 0, 1, 32, len(image), offset))
            payload.append(image)
            offset += len(image)
        (ICONS / "app.ico").write_bytes(struct.pack("<HHH", 0, 1, len(images)) + b"".join(entries + payload))
        iconset = temp / "grpvn.iconset"
        iconset.mkdir()
        for size in (16, 32, 128, 256, 512):
            shutil.copyfile(temp / f"{size}.png", iconset / f"icon_{size}x{size}.png")
            shutil.copyfile(temp / f"{size * 2}.png", iconset / f"icon_{size}x{size}@2x.png")
        subprocess.run(["iconutil", "-c", "icns", "-o", str(ICONS / "grpvn.icns"), str(iconset)], check=True)
    print("Generated grpvn web, brand, macOS, Windows and tray icons.")


if __name__ == "__main__":
    main()

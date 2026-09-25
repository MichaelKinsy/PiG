#!/usr/bin/env python3
"""Check every deliverable's codec, size, dimensions, decodeability and faststart atom order."""
import argparse
import hashlib
import json
from pathlib import Path
import struct
import subprocess

DEMO = Path(__file__).resolve().parent


def atoms(data):
    result = []
    offset = 0
    while offset < len(data):
        size, kind = struct.unpack_from(">I4s", data, offset)
        if size == 1:
            size = struct.unpack_from(">Q", data, offset + 8)[0]
        elif size == 0:
            size = len(data) - offset
        if size < 8 or offset + size > len(data):
            raise ValueError("Invalid MP4 atom size")
        result.append(kind.decode("ascii"))
        offset += size
    return result


def deliverables(rehearsal=False):
    steps = "abcdef" if rehearsal else "abcde"
    clips = [*(f"demo-{step}.mp4" for step in steps), "demo-hook.mp4"]
    posters = [*(f"demo-{step}.png" for step in steps), "demo-hook.png"]
    if rehearsal:
        clips += ["demo-full.mp4", "demo-full.gif"]
        posters += ["demo-full.png"]
    return clips + posters


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--tools", type=Path, required=True)
    ap.add_argument("--write", action="store_true", help="write the reviewed media inventory")
    ap.add_argument("--rehearsal", action="store_true", help="also validate local, uncleared game footage; never publish it")
    args = ap.parse_args()
    inventory = []
    for name in deliverables(args.rehearsal):
        path = DEMO / name
        data = path.read_bytes()
        info = json.loads(subprocess.check_output([str(args.tools / "ffprobe"), "-v", "error", "-show_format", "-show_streams", "-of", "json", str(path)]))
        stream = info["streams"][0]
        if len(info["streams"]) != 1 or (stream["width"], stream["height"]) != (1280, 720):
            raise RuntimeError(f"{name}: expected one 1280x720 video/image stream")
        row = {"file": name, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest(), "width": 1280, "height": 720,
               "codec": stream["codec_name"]}
        if path.suffix == ".mp4":
            if stream["codec_name"] != "h264" or stream["pix_fmt"] != "yuv420p" or len(data) >= 10_000_000:
                raise RuntimeError(f"{name}: expected H.264/yuv420p under 10 MB")
            order = atoms(data)
            if order.index("moov") > order.index("mdat"):
                raise RuntimeError(f"{name}: faststart moov atom does not precede mdat")
            duration = float(info["format"]["duration"])
            if name == "demo-hook.mp4" and abs(duration - 5) > 0.001:
                raise RuntimeError(f"Hook must be exactly five seconds, got {duration}")
            row.update(duration_seconds=duration, pixel_format=stream["pix_fmt"], faststart=True)
        subprocess.run([str(args.tools / "ffmpeg"), "-v", "error", "-i", str(path), "-f", "null", "-"],
                       check=True, stdout=subprocess.DEVNULL)
        inventory.append(row)
    manifest = DEMO / ("media-rehearsal.json" if args.rehearsal else "media.json")
    if args.write:
        manifest.write_text(json.dumps(inventory, indent=2) + "\n")
    elif json.loads(manifest.read_text()) != inventory:
        raise RuntimeError("Media differs from the reviewed inventory; regenerate using encode.py")
    print(f"PASS: {len(inventory)} assets; every codec, size, dimension, decode and faststart check passed.")


if __name__ == "__main__":
    main()

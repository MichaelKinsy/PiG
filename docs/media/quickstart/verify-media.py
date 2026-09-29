#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Check the quickstart's delivery constraints without third-party Python packages."""
import json
from pathlib import Path
import struct
import subprocess
import sys


def probe(path):
    return json.loads(subprocess.check_output([
        "ffprobe", "-v", "error", "-show_streams", "-show_format", "-of", "json", str(path)
    ]))


def first_frame(path):
    return subprocess.check_output([
        "ffmpeg", "-v", "error", "-i", str(path), "-frames:v", "1",
        "-pix_fmt", "rgb24", "-f", "rawvideo", "-"
    ])


def verify(directory):
    mp4 = directory / "quickstart.mp4"
    gif = directory / "quickstart.gif"
    poster = directory / "quickstart-poster.png"
    for path, ceiling in ((mp4, 8_000_000), (gif, 6_000_000)):
        assert 0 < path.stat().st_size < ceiling, f"{path.name}: file size exceeds delivery limit"
    video = probe(mp4)
    animation = probe(gif)
    image = probe(poster)
    stream = video["streams"][0]
    assert stream["codec_name"] == "h264", stream
    assert stream["pix_fmt"] == "yuv420p", stream
    assert animation["streams"][0]["codec_name"] == "gif", animation
    assert image["streams"][0]["codec_name"] == "png", image
    for metadata in (video, animation, image):
        assert (metadata["streams"][0]["width"], metadata["streams"][0]["height"]) == (1280, 720)
    duration = float(video["format"]["duration"])
    assert 60 <= duration <= 90, f"duration {duration} is outside 60–90 seconds"
    assert abs(float(animation["format"]["duration"]) - duration) < 0.25
    assert all(s["codec_type"] == "video" for s in video["streams"]), "unexpected audio/data stream"
    # Read top-level ISO BMFF boxes rather than matching strings inside compressed data.
    data = mp4.read_bytes()
    boxes = []
    offset = 0
    while offset < len(data):
        size, kind = struct.unpack_from(">I4s", data, offset)
        if size == 1:
            size = struct.unpack_from(">Q", data, offset + 8)[0]
        elif size == 0:
            size = len(data) - offset
        assert size >= 8 and offset + size <= len(data), "invalid MP4 box"
        boxes.append(kind)
        offset += size
    assert boxes.index(b"moov") < boxes.index(b"mdat"), "MP4 is not faststart"
    assert first_frame(mp4) == first_frame(poster), "poster is not the first decoded video frame"
    for path in (mp4, gif, poster):
        print(f"{path.name}: {path.stat().st_size} bytes")
    print(f"PASS: {duration:.3f}s, 1280x720, H.264/yuv420p, faststart, GIF, first-frame poster")


if __name__ == "__main__":
    verify(Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).resolve().parent)

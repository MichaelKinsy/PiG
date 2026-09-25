#!/usr/bin/env python3
"""Encode local checked takes; this command never publishes media."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess

DEMO = Path(__file__).resolve().parent
LABELS = {
    "a": "LOCAL REHEARSAL · FAUX PROVIDER  |  A · Pi / TypeScript chain",
    "b": "LOCAL REHEARSAL · FAUX PROVIDER  |  B · PiG / identical TypeScript chain",
    "c": "LOCAL REHEARSAL · NO MODEL CALLS  |  C · Go + Rust Piglet builds / warm cache",
    "d": "LOCAL REHEARSAL · FAUX PROVIDER  |  D · Clean HOME / same Linux host / empty PATH",
    "e": "LOCAL REHEARSAL · NO MODEL CALLS  |  E · PiG Runner / fused Go extension",
    "f": "LOCAL REHEARSAL  |  F BLOCKED · Rust + reload work; Doom frame/HUD are cropped",
}


def run(command, log):
    subprocess.run([str(arg) for arg in command], check=True, stdout=log, stderr=log)


def probe(ffprobe, path):
    return json.loads(subprocess.check_output([str(ffprobe), "-v", "error", "-show_format", "-show_streams", "-of", "json", str(path)]))


def label_file(path, label):
    path.write_text("[Script Info]\nScriptType: v4.00+\nPlayResX: 1280\nPlayResY: 720\n"
                    "[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n"
                    "Style: Default,DejaVu Sans,20,&H00E7F1E7,&H00FFFFFF,&H00151521,&H00151521,0,0,0,0,100,100,0,0,1,0,0,7,20,20,11,1\n"
                    "[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n"
                    f"Dialogue: 0,0:00:00.00,1:00:00.00,Default,,0,0,0,,{label}\n")


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--scratch", type=Path, required=True)
    ap.add_argument("--tools", type=Path, required=True)
    args = ap.parse_args()
    scratch, tools = args.scratch.resolve(), args.tools.resolve()
    ffmpeg, ffprobe = tools / "ffmpeg", tools / "ffprobe"
    codec = ["-an", "-c:v", "libx264", "-preset", "slow", "-crf", "23", "-pix_fmt", "yuv420p",
             "-r", "20", "-movflags", "+faststart", "-map_metadata", "-1"]
    evidence_dir = DEMO / "evidence"
    evidence_dir.mkdir(exist_ok=True)
    with (scratch / "encode.log").open("w") as log:
        for step, label in LABELS.items():
            evidence = json.loads((scratch / f"evidence-{step}.json").read_text())
            raw = scratch / f"raw-{step}.mp4"
            if hashlib.sha256(raw.read_bytes()).hexdigest() != evidence["raw_sha256"]:
                raise RuntimeError(f"Take {step} changed since its checks")
            if evidence["status"] != ("BLOCKED" if step == "f" else "PASS"):
                raise RuntimeError(f"Unexpected take status: {evidence['status']}")
            ass = scratch / f"label-{step}.ass"
            label_file(ass, label)
            output = DEMO / f"demo-{step}.mp4"
            video_filter = f"fps=20,scale=1200:676:flags=lanczos,pad=1280:720:40:44:color=0x151521,ass='{ass}'"
            run([ffmpeg, "-y", "-i", raw, "-vf", video_filter, *codec, output], log)
            (evidence_dir / f"step-{step}.json").write_text(json.dumps(evidence, indent=2, ensure_ascii=False) + "\n")
        listing = scratch / "concat.txt"
        listing.write_text("".join(f"file '{DEMO / f'demo-{step}.mp4'}'\n" for step in LABELS))
        full = DEMO / "demo-full.mp4"
        run([ffmpeg, "-y", "-f", "concat", "-safe", "0", "-i", listing, "-c", "copy", "-movflags", "+faststart", full], log)
        # Two unsped-up excerpts: the Pi chain result, then a real Runner jump. No Doom success is implied.
        run([ffmpeg, "-y", "-ss", "14", "-t", "2.5", "-i", DEMO / "demo-a.mp4",
             "-ss", "7.5", "-t", "2.5", "-i", DEMO / "demo-e.mp4",
             "-filter_complex", "[0:v]setpts=PTS-STARTPTS[a];[1:v]setpts=PTS-STARTPTS[b];[a][b]concat=n=2:v=1:a=0[v]",
             "-map", "[v]", *codec, DEMO / "demo-hook.mp4"], log)
        run([ffmpeg, "-y", "-i", full, "-filter_complex",
             "fps=6,split[a][b];[a]palettegen=max_colors=128:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=3",
             "-loop", "0", DEMO / "demo-full.gif"], log)
        posters = {"a": 14, "b": 15, "c": -2, "d": 14, "e": 9, "f": 34, "full": 14, "hook": 3.9}
        for name, when in posters.items():
            source = DEMO / f"demo-{name}.mp4"
            if when < 0:
                when += float(probe(ffprobe, source)["format"]["duration"])
            run([ffmpeg, "-y", "-ss", str(when), "-i", source, "-frames:v", "1", DEMO / f"demo-{name}.png"], log)
    for mode in [[], ["--rehearsal"]]:
        subprocess.run(["python3", "-B", str(DEMO / "verify-media.py"), "--tools", str(tools), "--write", *mode], check=True)


if __name__ == "__main__":
    main()

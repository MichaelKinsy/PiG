#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Michael Kinsy
# SPDX-License-Identifier: MIT
"""Regenerates testdata/webp/*.webp: files written by libwebp, the encoder most WebP files come from, for the corpus of
webpdecode_corpus_test.go (real bitstreams: the segments, the loop filters, the partitions, the alpha filters and the lossless transforms of
an encoder, next to the synthetic streams of the Go writers).

  python3 webpdecode_fixtures.py testdata/webp

It binds libwebp through ctypes (libwebp.so.7, version 1.5.0 when the files were made: WebPGetEncoderVersion() is printed), with the
WebPConfig and WebPPicture structures of its encode.h declared here. The pictures are integer-only, deterministic: value noise with a
few shapes, as in corpus.mjs.
"""
import ctypes
import os
import sys

lib = ctypes.CDLL("libwebp.so.7")
ABI = 0x020F


class Config(ctypes.Structure):
    _fields_ = [(n, ctypes.c_int if n not in ("quality", "target_PSNR") else ctypes.c_float) for n in (
        "lossless", "quality", "method", "image_hint", "target_size", "target_PSNR", "segments", "sns_strength",
        "filter_strength", "filter_sharpness", "filter_type", "autofilter", "alpha_compression", "alpha_filtering",
        "alpha_quality", "pass_", "show_compressed", "preprocessing", "partitions", "partition_limit", "emulate_jpeg_size",
        "thread_level", "low_memory", "near_lossless", "exact", "use_delta_palette", "use_sharp_yuv", "qmin", "qmax")] + [("pad", ctypes.c_uint32 * 8)]


class MemoryWriter(ctypes.Structure):
    _fields_ = [("mem", ctypes.c_void_p), ("size", ctypes.c_size_t), ("max_size", ctypes.c_size_t), ("pad", ctypes.c_uint32 * 2)]


WRITER = ctypes.CFUNCTYPE(ctypes.c_int, ctypes.POINTER(ctypes.c_uint8), ctypes.c_size_t, ctypes.c_void_p)


class Picture(ctypes.Structure):
    _fields_ = [
        ("use_argb", ctypes.c_int), ("colorspace", ctypes.c_int), ("width", ctypes.c_int), ("height", ctypes.c_int),
        ("y", ctypes.c_void_p), ("u", ctypes.c_void_p), ("v", ctypes.c_void_p), ("y_stride", ctypes.c_int), ("uv_stride", ctypes.c_int),
        ("a", ctypes.c_void_p), ("a_stride", ctypes.c_int), ("pad1", ctypes.c_uint32 * 2),
        ("argb", ctypes.c_void_p), ("argb_stride", ctypes.c_int), ("pad2", ctypes.c_uint32 * 3),
        ("writer", ctypes.c_void_p), ("custom_ptr", ctypes.c_void_p), ("extra_info_type", ctypes.c_int), ("extra_info", ctypes.c_void_p),
        ("stats", ctypes.c_void_p), ("error_code", ctypes.c_int), ("progress_hook", ctypes.c_void_p), ("user_data", ctypes.c_void_p),
        ("pad3", ctypes.c_uint32 * 3), ("pad4", ctypes.c_void_p), ("pad5", ctypes.c_void_p), ("pad6", ctypes.c_uint32 * 8),
        ("memory_", ctypes.c_void_p), ("memory_argb_", ctypes.c_void_p), ("pad7", ctypes.c_void_p * 2), ("slack", ctypes.c_uint8 * 256)]


def hash32(a):
    a = ((a ^ (a >> 16)) * 0x45D9F3B) & 0xFFFFFFFF
    a = ((a ^ (a >> 16)) * 0x45D9F3B) & 0xFFFFFFFF
    return (a ^ (a >> 16)) & 0xFFFFFFFF


def lattice(channel, octave, size, x, y):
    def value(i, j):
        return hash32(((i * 73856093) ^ (j * 19349663) ^ ((channel + 1) * 83492791) ^ ((octave + 1) * 668265263)) & 0xFFFFFFFF) >> 24
    i, j, fx, fy = x // size, y // size, x % size, y % size
    total = value(i, j) * (size - fx) * (size - fy) + value(i + 1, j) * fx * (size - fy) + value(i, j + 1) * (size - fx) * fy + value(i + 1, j + 1) * fx * fy
    return total // (size * size)


def picture(kind, w, h):
    """RGBA bytes of a w x h picture: photo (value noise), flat (a few flat colors: palette-friendly), shapes (hard edges, alpha)."""
    out = bytearray()
    for y in range(h):
        for x in range(w):
            if kind == "photo":
                px = [min(255, max(0, (lattice(c, 0, 32, x, y) * 5 + lattice(c, 1, 8, x, y) * 3) >> 3)) for c in range(3)] + [255]
            elif kind == "flat":
                k = ((x // 7) + (y // 5)) % 5
                px = [(60 * k) & 255, (255 - 40 * k) & 255, (90 + 30 * k) & 255, 255]
            else:  # shapes with alpha
                d = (x - w // 2) ** 2 + (y - h // 2) ** 2
                inside = d < (min(w, h) // 3) ** 2
                px = [200 if inside else 20, (x * 255 // max(1, w - 1)), (y * 255 // max(1, h - 1)), 255 if inside else (x * 255 // max(1, w - 1)) ^ 0x55]
            out += bytes(px)
    return bytes(out)


def encode(rgba, w, h, **options):
    config = Config()
    assert lib.WebPConfigInitInternal(ctypes.byref(config), 0, ctypes.c_float(75.0), ABI)
    for name, value in options.items():
        setattr(config, name.rstrip("_") if name != "pass_" else "pass_", value)
    pic = Picture()
    assert lib.WebPPictureInitInternal(ctypes.byref(pic), ABI)
    pic.width, pic.height, pic.use_argb = w, h, 1
    buf = ctypes.create_string_buffer(rgba, len(rgba))
    assert lib.WebPPictureImportRGBA(ctypes.byref(pic), buf, w * 4)
    mw = MemoryWriter()
    lib.WebPMemoryWriterInit(ctypes.byref(mw))
    pic.writer = ctypes.cast(lib.WebPMemoryWrite, ctypes.c_void_p).value
    pic.custom_ptr = ctypes.addressof(mw)
    ok = lib.WebPEncode(ctypes.byref(config), ctypes.byref(pic))
    assert ok, "WebPEncode failed with error %d" % pic.error_code
    data = ctypes.string_at(mw.mem, mw.size)
    lib.WebPPictureFree(ctypes.byref(pic))
    lib.WebPMemoryWriterClear(ctypes.byref(mw))
    return data


# name, picture kind, width, height, WebPConfig fields
FIXTURES = [
    ("lossy-photo-64x48-q75", "photo", 64, 48, {}),
    ("lossy-photo-1x1", "photo", 1, 1, {}),
    ("lossy-photo-3x2", "photo", 3, 2, {}),
    ("lossy-photo-17x15", "photo", 17, 15, {}),
    ("lossy-photo-129x97-q30-nofilter", "photo", 129, 97, {"quality": 30.0, "filter_strength": 0, "segments": 1, "sns_strength": 0}),
    ("lossy-photo-130x90-simple-sharp3", "photo", 130, 90, {"filter_type": 0, "filter_strength": 60, "filter_sharpness": 3, "quality": 60.0}),
    ("lossy-photo-130x90-strong-sharp7", "photo", 130, 90, {"filter_type": 1, "filter_strength": 100, "filter_sharpness": 7, "quality": 40.0}),
    ("lossy-photo-200x130-q90-8partitions", "photo", 200, 130, {"quality": 90.0, "partitions": 3}),
    ("lossy-photo-150x100-q50-method6-autofilter", "photo", 150, 100, {"quality": 50.0, "method": 6, "autofilter": 1, "segments": 4}),
    ("lossy-photo-96x64-q100", "photo", 96, 64, {"quality": 100.0}),
    ("lossy-photo-96x64-q1", "photo", 96, 64, {"quality": 1.0}),
    ("lossy-photo-120x80-sharpyuv", "photo", 120, 80, {"use_sharp_yuv": 1}),
    ("lossy-shapes-100x60", "shapes", 100, 60, {"quality": 70.0}),
    ("lossy-flat-80x50", "flat", 80, 50, {"quality": 80.0}),
    ("lossy-alpha-photo-90x70-raw", "shapes", 90, 70, {"alpha_compression": 0, "alpha_filtering": 0}),
    ("lossy-alpha-90x70-lossless-nofilter", "shapes", 90, 70, {"alpha_compression": 1, "alpha_filtering": 0}),
    ("lossy-alpha-90x70-lossless-fast", "shapes", 90, 70, {"alpha_compression": 1, "alpha_filtering": 1}),
    ("lossy-alpha-90x70-lossless-best", "shapes", 90, 70, {"alpha_compression": 1, "alpha_filtering": 2}),
    ("lossy-alpha-90x70-quality-20", "shapes", 90, 70, {"alpha_compression": 1, "alpha_filtering": 2, "alpha_quality": 20}),
    ("lossless-photo-64x48", "photo", 64, 48, {"lossless": 1, "quality": 75.0}),
    ("lossless-photo-1x1", "photo", 1, 1, {"lossless": 1}),
    ("lossless-photo-33x31-q0-method0", "photo", 33, 31, {"lossless": 1, "quality": 0.0, "method": 0}),
    ("lossless-photo-120x80-q100-method6", "photo", 120, 80, {"lossless": 1, "quality": 100.0, "method": 6}),
    ("lossless-flat-80x50-palette", "flat", 80, 50, {"lossless": 1, "quality": 100.0, "method": 4}),
    ("lossless-flat-79x49-palette", "flat", 79, 49, {"lossless": 1, "quality": 60.0, "method": 3}),
    ("lossless-shapes-100x60-alpha", "shapes", 100, 60, {"lossless": 1, "exact": 1}),
    ("lossless-photo-100x70-nearlossless60", "photo", 100, 70, {"lossless": 1, "near_lossless": 60}),
    ("lossless-photo-150x100-cache", "photo", 150, 100, {"lossless": 1, "quality": 90.0, "method": 5}),
]


def main():
    out = sys.argv[1]
    os.makedirs(out, exist_ok=True)
    print("libwebp encoder version %#x" % lib.WebPGetEncoderVersion())
    for name, kind, w, h, options in FIXTURES:
        data = encode(picture(kind, w, h), w, h, **options)
        with open(os.path.join(out, name + ".webp"), "wb") as f:
            f.write(data)
        print(name, len(data))


main()

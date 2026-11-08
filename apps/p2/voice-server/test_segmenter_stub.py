"""Pre-roll test for segmenter.py without sherpa-onnx or speech models (they cannot be fetched reproducibly here).

Ports the intention of test_segmenter.py: drive the vendored segmenter.py with synthetic frames and a stub VAD that
mimics sherpa-onnx's documented behaviour (the segment starts 1024 + min_speech samples before the detection window,
older audio is dropped). Checks: the pre-roll keeps a quiet first word that sherpa alone loses, the utterance is a
contiguous slice of the input, and the pre-roll never reaches into the previous utterance.
Run: make -C apps/p2 voice-segmenter-test (python:3.12-slim, numpy==2.5.4 pinned).
"""

import sys
import types

import numpy as np

SR = 16000
W = 512


class VadModelConfig:
    def __init__(self):
        self.silero_vad = types.SimpleNamespace(min_speech_duration=0.25, min_silence_duration=1.2)
        self.sample_rate = SR


class VoiceActivityDetector:
    """Energy VAD: speech when a window's peak exceeds 0.05 (a -30 dB quiet word never does)."""

    def __init__(self, config, buffer_size_in_seconds=60):
        self.cfg = config.silero_vad
        self.reset()

    def reset(self):
        self.buf = np.zeros(0, dtype=np.float32)
        self.segments = []
        self.hot = 0
        self.detected = False
        self.last_loud = 0
        self.start = 0

    def accept_waveform(self, w):
        self.buf = np.concatenate([self.buf, w])
        n = len(self.buf)
        if np.abs(w).max() > 0.05:
            self.hot += 1
            self.last_loud = n
            if not self.detected and self.hot * W >= self.cfg.min_speech_duration * SR:
                self.detected = True
                self.start = max(0, n - 2 * W - int(self.cfg.min_speech_duration * SR))
        elif self.detected and n - self.last_loud >= self.cfg.min_silence_duration * SR:
            seg = types.SimpleNamespace(start=self.start, samples=self.buf[self.start : self.last_loud].copy())
            self.segments.append(seg)
            self.detected = False
            self.hot = 0
        elif not self.detected:
            self.hot = 0

    def is_speech_detected(self):
        return self.detected

    def empty(self):
        return not self.segments

    @property
    def front(self):
        return self.segments[0]

    def pop(self):
        self.segments.pop(0)


sys.modules["sherpa_onnx"] = types.SimpleNamespace(VadModelConfig=VadModelConfig, VoiceActivityDetector=VoiceActivityDetector)
from segmenter import Segmenter  # noqa: E402


def tone(seconds, amp, seed):
    t = np.arange(int(seconds * SR)) / SR
    return (amp * np.sin(2 * np.pi * (200 + 37 * seed) * t) * (1 + 0.3 * np.sin(2 * np.pi * 3 * t))).astype(np.float32)


def run(audio, preroll_ms):
    seg = Segmenter(VadModelConfig(), preroll_ms)
    out = []
    for off in range(0, len(audio), 1600):
        out += [u for ev, u in seg.accept(audio[off : off + 1600]) if u is not None]
    return out


def locate(audio, u):
    for s in range(0, len(audio) - len(u) + 1):
        if audio[s] == u[0] and np.array_equal(audio[s : s + len(u)], u):
            return s
    raise AssertionError("utterance is not a contiguous slice of the input")


def main():
    rng = np.random.default_rng(1)
    word, rest = tone(0.2, 0.015, 1), tone(1.5, 0.4, 2)
    parts = [np.zeros(int(1.5 * SR)), word, np.zeros(int(0.15 * SR)), rest, np.zeros(2 * SR)]
    audio = np.concatenate(parts).astype(np.float32)
    audio += rng.normal(0, 0.0005, len(audio)).astype(np.float32)
    onset = int(1.5 * SR)
    starts = {}
    for pre in (0, 750):
        us = run(audio, pre)
        assert len(us) == 1, f"preroll {pre}: {len(us)} utterances"
        starts[pre] = locate(audio, us[0])
    assert starts[0] > onset + len(word), "stub VAD too sensitive: sherpa alone kept the quiet word"
    assert starts[750] <= onset, f"pre-roll did not reach the quiet first word ({starts[750]} > {onset})"
    assert starts[0] - starts[750] > 0

    # Never reaches into the previous utterance: two utterances 1.3 s apart, pre-roll far larger than the gap.
    a, b = tone(1.0, 0.4, 3), tone(1.0, 0.4, 4)
    two = np.concatenate([np.zeros(SR), a, np.zeros(int(1.4 * SR)), b, np.zeros(2 * SR)]).astype(np.float32)
    us = run(two, 3000)
    assert len(us) == 2, f"{len(us)} utterances"
    s0, s1 = locate(two, us[0]), locate(two, us[1])
    assert s1 >= s0 + len(us[0]), "second utterance reaches into the first"
    print("ok")
    return 0


if __name__ == "__main__":
    sys.exit(main())

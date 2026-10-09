package protocol

import (
	"errors"
	"math"
	"testing"
)

func requireFrameError(t *testing.T, err error, want string) {
	t.Helper()
	frame, ok := errors.AsType[*FrameError](err)
	if !ok || frame.Message != want {
		t.Fatalf("error=%T %v, want FrameError %q", err, err, want)
	}
}

// Pi: packages/protocol/src/framing.ts:3-4,18-23 (default limit, resolveMaxFrameLength and its message).
func TestFrameDecoderLimitDefaultsAndRange(t *testing.T) {
	t.Parallel()
	if DefaultMaxFrameLength != 16777216 {
		t.Fatalf("DefaultMaxFrameLength=%d", DefaultMaxFrameLength)
	}
	decoder := requireFrameDecoder(t, FrameDecoderOptions{})
	_, err := decoder.Push([]byte{0x01, 0x00, 0x00, 0x01})
	requireFrameError(t, err, "Frame length 16777217 exceeds configured limit of 16777216")
	for _, value := range []float64{-1, 0.5, math.NaN(), math.Inf(1), 4294967296} {
		_, err := NewFrameDecoder(FrameDecoderOptions{MaxFrameLength: &value})
		rangeErr, ok := errors.AsType[*RangeError](err)
		if !ok || rangeErr.Message != "maxFrameLength must be an integer between 0 and 4294967295" {
			t.Fatalf("limit %v error=%T %v", value, err, err)
		}
	}
	// The bounds are inclusive: 0 and 4294967295 are accepted (the second only declares a frame, no payload is allocated up front).
	for _, value := range []float64{0, math.Copysign(0, -1), 4294967295} {
		if _, err := NewFrameDecoder(FrameDecoderOptions{MaxFrameLength: &value}); err != nil {
			t.Fatalf("limit %v rejected: %v", value, err)
		}
	}
	huge := requireFrameDecoder(t, FrameDecoderOptions{MaxFrameLength: new(4294967295.0)})
	if frames := requireFrames(t, huge, []byte{0xff, 0xff, 0xff, 0xff, 1, 2, 3}); len(frames) != 0 {
		t.Fatalf("frames=%v", frames)
	}
	requireFrameError(t, huge.End(), "Truncated frame at end of stream")
}

// Pi: framing.ts:92-110,139-146: a zero limit admits only empty frames; a frame equal to the limit is accepted.
func TestFrameDecoderZeroAndExactLimit(t *testing.T) {
	t.Parallel()
	zero := requireFrameDecoder(t, FrameDecoderOptions{MaxFrameLength: new(0.0)})
	equalFrames(t, requireFrames(t, zero, []byte{0, 0, 0, 0}), [][]byte{{}})
	_, err := zero.Push([]byte{0, 0, 0, 1})
	requireFrameError(t, err, "Frame length 1 exceeds configured limit of 0")
	three := requireFrameDecoder(t, FrameDecoderOptions{MaxFrameLength: new(3.0)})
	equalFrames(t, requireFrames(t, three, []byte{0, 0, 0, 3, 7, 8, 9}), [][]byte{{7, 8, 9}})
	_, err = three.Push([]byte{0, 0, 0, 4})
	requireFrameError(t, err, "Frame length 4 exceeds configured limit of 3")
}

// Pi: framing.ts:126-148: a failed decoder reports "failed" from push and end, and a failure discards the partial frame.
func TestFrameDecoderFailedStateRejectsEveryOperation(t *testing.T) {
	t.Parallel()
	decoder := requireFrameDecoder(t, FrameDecoderOptions{MaxFrameLength: new(1.0)})
	_, err := decoder.Push([]byte{0, 0, 0, 2})
	requireFrameError(t, err, "Frame length 2 exceeds configured limit of 1")
	_, err = decoder.Push([]byte{0})
	requireFrameError(t, err, "Frame decoder has failed")
	requireFrameError(t, decoder.End(), "Frame decoder has failed")
	// A failure inside a chunk drops the frames already completed in that chunk (the TypeScript push throws before returning them).
	decoder = requireFrameDecoder(t, FrameDecoderOptions{MaxFrameLength: new(1.0)})
	frames, err := decoder.Push([]byte{0, 0, 0, 1, 5, 0, 0, 0, 2})
	if frames != nil {
		t.Fatalf("frames=%v", frames)
	}
	requireFrameError(t, err, "Frame length 2 exceeds configured limit of 1")
}

package protocol

// pi: packages/protocol/src/framing.ts

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
)

func requireFrame(t *testing.T, payload []byte) []byte {
	t.Helper()
	frame, err := EncodeFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}
func requireFrameDecoder(t *testing.T, options FrameDecoderOptions) *FrameDecoder {
	t.Helper()
	decoder, err := NewFrameDecoder(options)
	if err != nil {
		t.Fatal(err)
	}
	return decoder
}
func requireFrames(t *testing.T, decoder *FrameDecoder, input []byte) [][]byte {
	t.Helper()
	frames, err := decoder.Push(input)
	if err != nil {
		t.Fatal(err)
	}
	return frames
}
func endFrames(t *testing.T, decoder *FrameDecoder) {
	t.Helper()
	if err := decoder.End(); err != nil {
		t.Fatal(err)
	}
}
func equalFrames(t *testing.T, got, want [][]byte) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frames = %v, want %v", got, want)
	}
}

func TestFraming(t *testing.T) {
	t.Parallel()
	// upstream: packages/protocol/test/framing.test.ts:15.
	t.Run("prefixes payloads with a four-byte big-endian length", func(t *testing.T) {
		t.Parallel()
		equalFrames(t, [][]byte{requireFrame(t, []byte{0xaa, 0xbb, 0xcc}), requireFrame(t, []byte{})}, [][]byte{{0, 0, 0, 3, 0xaa, 0xbb, 0xcc}, {0, 0, 0, 0}})
	})
	// upstream: packages/protocol/test/framing.test.ts:22.
	t.Run("decodes fragmented, coalesced, and empty frames in order", func(t *testing.T) {
		t.Parallel()
		wire := append(append(requireFrame(t, []byte{1, 2, 3}), requireFrame(t, []byte{})...), requireFrame(t, []byte{4})...)
		decoder := requireFrameDecoder(t, FrameDecoderOptions{})
		frames := [][]byte{}
		for _, b := range wire {
			frames = append(frames, requireFrames(t, decoder, []byte{b})...)
		}
		endFrames(t, decoder)
		equalFrames(t, frames, [][]byte{{1, 2, 3}, {}, {4}})
		coalesced := requireFrameDecoder(t, FrameDecoderOptions{})
		equalFrames(t, requireFrames(t, coalesced, wire), frames)
		endFrames(t, coalesced)
	})
	// upstream: packages/protocol/test/framing.test.ts:39.
	t.Run("assembles payloads spanning multiple internal blocks", func(t *testing.T) {
		t.Parallel()
		payload := make([]byte, 70_000)
		for i := range payload {
			payload[i] = byte(i % 251)
		}
		wire := requireFrame(t, payload)
		decoder := requireFrameDecoder(t, FrameDecoderOptions{})
		frames := append(append(requireFrames(t, decoder, wire[:101]), requireFrames(t, decoder, wire[101:65_541])...), requireFrames(t, decoder, wire[65_541:])...)
		endFrames(t, decoder)
		equalFrames(t, frames, [][]byte{payload})
	})
	// upstream: packages/protocol/test/framing.test.ts:52.
	t.Run("handles every split point across a frame", func(t *testing.T) {
		t.Parallel()
		wire := requireFrame(t, []byte{10, 20, 30, 40})
		for split := 0; split <= len(wire); split++ {
			t.Run(fmt.Sprint(split), func(t *testing.T) {
				t.Parallel()
				decoder := requireFrameDecoder(t, FrameDecoderOptions{})
				frames := append(requireFrames(t, decoder, wire[:split]), requireFrames(t, decoder, wire[split:])...)
				endFrames(t, decoder)
				equalFrames(t, frames, [][]byte{{10, 20, 30, 40}})
			})
		}
	})
	// upstream: packages/protocol/test/framing.test.ts:62.
	t.Run("copies payload bytes instead of retaining or aliasing input chunks", func(t *testing.T) {
		t.Parallel()
		chunk := requireFrame(t, []byte{1, 2, 3})
		decoder := requireFrameDecoder(t, FrameDecoderOptions{})
		frames := requireFrames(t, decoder, chunk)
		for i := range chunk {
			chunk[i] = 9
		}
		equalFrames(t, frames, [][]byte{{1, 2, 3}})
	})
	// upstream: packages/protocol/test/framing.test.ts:70.
	t.Run("accepts empty chunks and a clean empty stream", func(t *testing.T) {
		t.Parallel()
		decoder := requireFrameDecoder(t, FrameDecoderOptions{})
		equalFrames(t, requireFrames(t, decoder, []byte{}), [][]byte{})
		endFrames(t, decoder)
	})
	// upstream: packages/protocol/test/framing.test.ts:76.
	for _, test := range []struct {
		name string
		wire []byte
	}{{"partial header", []byte{0, 0, 0}}, {"partial payload", []byte{0, 0, 0, 2, 1}}} {
		t.Run("rejects a truncated stream at end: "+test.name, func(t *testing.T) {
			t.Parallel()
			decoder := requireFrameDecoder(t, FrameDecoderOptions{})
			equalFrames(t, requireFrames(t, decoder, test.wire), [][]byte{})
			if _, ok := errors.AsType[*FrameError](decoder.End()); !ok {
				t.Fatal("expected FrameError for truncated stream")
			}
		})
	}
	// upstream: packages/protocol/test/framing.test.ts:85.
	t.Run("rejects an oversized declared length as soon as its header is complete", func(t *testing.T) {
		t.Parallel()
		decoder := requireFrameDecoder(t, FrameDecoderOptions{MaxFrameLength: new(3.0)})
		if _, err := decoder.Push([]byte{0, 0, 0, 4}); err == nil || err.Error() != "Frame length 4 exceeds configured limit of 3" {
			t.Fatalf("oversized error = %v", err)
		}
		if _, err := decoder.Push([]byte{1}); err == nil || err.Error() != "Frame decoder has failed" {
			t.Fatalf("failed decoder error = %v", err)
		}
	})
	// upstream: packages/protocol/test/framing.test.ts:91.
	t.Run("accepts a frame exactly at the configured maximum", func(t *testing.T) {
		t.Parallel()
		decoder := requireFrameDecoder(t, FrameDecoderOptions{MaxFrameLength: new(3.0)})
		equalFrames(t, requireFrames(t, decoder, requireFrame(t, []byte{1, 2, 3})), [][]byte{{1, 2, 3}})
		endFrames(t, decoder)
	})
	// upstream: packages/protocol/test/framing.test.ts:97.
	t.Run("cannot be pushed after end", func(t *testing.T) {
		t.Parallel()
		decoder := requireFrameDecoder(t, FrameDecoderOptions{})
		endFrames(t, decoder)
		if _, err := decoder.Push([]byte{}); err == nil || err.Error() != "Frame decoder has ended" {
			t.Fatalf("Push after end = %v", err)
		}
		if err := decoder.End(); err == nil || err.Error() != "Frame decoder has ended" {
			t.Fatalf("End after end = %v", err)
		}
	})
	// upstream: packages/protocol/test/framing.test.ts:104.
	for _, value := range []float64{-1, 1.5, math.NaN(), DefaultMaxFrameLength * 1_000} {
		t.Run(fmt.Sprintf("rejects invalid maximum frame length: %v", value), func(t *testing.T) {
			t.Parallel()
			if _, err := NewFrameDecoder(FrameDecoderOptions{MaxFrameLength: &value}); err == nil {
				t.Fatal("accepted invalid maximum")
			} else if _, ok := errors.AsType[*RangeError](err); !ok {
				t.Fatalf("error = %T, want RangeError", err)
			}
		})
	}
}

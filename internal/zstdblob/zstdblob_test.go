// SPDX-License-Identifier: MIT

package zstdblob

import (
	"bytes"
	"runtime"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	for _, raw := range [][]byte{[]byte("x"), bytes.Repeat([]byte("embedded text "), 5000)} {
		frame := Encode(raw)
		got, err := Decode(frame)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, raw) {
			t.Fatalf("round trip of %d bytes returned %d bytes", len(raw), len(got))
		}
	}
}

func TestDecodeRejectsDamage(t *testing.T) {
	frame := Encode(bytes.Repeat([]byte("embedded text "), 5000))
	if _, err := Decode(frame[:len(frame)/2]); err == nil {
		t.Fatal("a truncated frame decoded")
	}
	if _, err := Decode([]byte("not a frame")); err == nil {
		t.Fatal("garbage decoded")
	}
}

// Decoding must leave nothing behind: no goroutine and a result of exactly the recorded size.
func TestDecodeSizesResultExactlyAndLeavesNoGoroutines(t *testing.T) {
	raw := bytes.Repeat([]byte("embedded text "), 5000)
	frame := Encode(raw)
	before := runtime.NumGoroutine()
	got, err := Decode(frame)
	if err != nil {
		t.Fatal(err)
	}
	if after := runtime.NumGoroutine(); after != before {
		t.Fatalf("%d goroutines before Decode, %d after", before, after)
	}
	if cap(got) != len(raw) {
		t.Fatalf("result capacity %d, want %d", cap(got), len(raw))
	}
}

// An input above zstdCLIMinimum is encoded by the zstd command when there is one and by the library otherwise; either way the frame decodes to the input and records its size, and the command's frame is no larger than the library's.
func TestEncodeLargeInputRoundTripsAndIsNoLargerThanTheLibrary(t *testing.T) {
	var raw []byte
	for i := 0; len(raw) < 3*zstdCLIMinimum; i++ {
		raw = append(raw, []byte("line "+string(rune('a'+i%26))+" of embedded text, number "+string(rune('0'+i%10))+"\n")...)
	}
	frame := Encode(raw)
	got, err := Decode(frame)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("round trip: %v", err)
	}
	if _, ok := frameSize(frame); !ok {
		t.Fatal("the frame records no content size")
	}
	if library := encodeWithLibrary(raw); len(frame) > len(library) {
		t.Fatalf("Encode made %d bytes, the library %d", len(frame), len(library))
	}
}

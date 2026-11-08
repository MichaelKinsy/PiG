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

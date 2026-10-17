package codemode

import "testing"

// The decoder that places the halt check must size each immediate exactly as the WebAssembly binary format does. Expectations are the format's
// (core spec binary/types "limits", binary/instructions "memarg", multi-memory proposal): not what skipLimits and skipInstruction return.
//   - limits: 0x00 min  |  0x01 min max  (the maximum is present exactly when flag bit 0 is set)
//   - memarg: align offset, and when align has bit 6 set (multi-memory) a memory index sits between them
func TestSkipLimitsFollowsTheMaximumPresentFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
		want int
	}{
		{"min only", []byte{0x00, 0x01, 0xAA}, 2},
		{"min and max", []byte{0x01, 0x01, 0x02, 0xAA}, 3},
		{"multi-byte min and max", []byte{0x01, 0x80, 0x01, 0x80, 0x02, 0xAA}, 5},
		{"shared limits keep the max flag set", []byte{0x03, 0x01, 0x02, 0xAA}, 3},
	} {
		got, err := skipLimits(tc.in, 0)
		if err != nil || got != tc.want {
			t.Errorf("%s: skipLimits = %d, %v; want %d", tc.name, got, err, tc.want)
		}
	}
}

func TestSkipInstructionReadsTheMemoryIndexOfAMultiMemoryMemarg(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
		want int
	}{
		// i32.load align=2 offset=0
		{"single memory", []byte{0x28, 0x02, 0x00, 0xAA}, 3},
		// i32.load with align 2|0x40, memory index 1, offset 0
		{"explicit memory index", []byte{0x28, 0x42, 0x01, 0x00, 0xAA}, 4},
		// i64.store align=3 offset=300 (two-byte LEB)
		{"multi-byte offset", []byte{0x37, 0x03, 0xAC, 0x02, 0xAA}, 4},
	} {
		got, err := skipInstruction(tc.in, 0)
		if err != nil || got != tc.want {
			t.Errorf("%s: skipInstruction = %d, %v; want %d", tc.name, got, err, tc.want)
		}
	}
}

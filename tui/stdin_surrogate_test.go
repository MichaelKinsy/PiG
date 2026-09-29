package tui

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// .upstream/v0.87.1/packages/tui/src/stdin-buffer.ts:202-253 indexes strings by UTF-16 unit, including inside Meta/SS3/X10 sequences.
func TestStdinSupplementaryCharacterEventUnits(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  [][]uint16
	}{
		{"😀", [][]uint16{{0xd83d}, {0xde00}}},
		{"A😀B", [][]uint16{{'A'}, {0xd83d}, {0xde00}, {'B'}}},
		{"\x1b😀", [][]uint16{{0x1b, 0xd83d}, {0xde00}}},
		{"\x1bO😀", [][]uint16{{0x1b, 'O', 0xd83d}, {0xde00}}},
		{"\x1b[M😀AB", [][]uint16{{0x1b, '[', 'M', 0xd83d, 0xde00, 'A'}, {'B'}}},
		{"\x1b[128512u😀", [][]uint16{{0x1b, '[', '1', '2', '8', '5', '1', '2', 'u'}, {0xd83d}, {0xde00}}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			b := NewStdinBuffer(StdinBufferOptions{})
			got := b.ProcessString(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("events=%q count=%d want=%04x", got, len(got), tc.want)
			}
			for i, part := range got {
				if units := jsstring.ToUTF16(part); !slices.Equal(units, tc.want[i]) {
					t.Errorf("event%d=%04x want=%04x", i, units, tc.want[i])
				}
			}
		})
	}
}

func TestStdinSupplementaryTerminalByteSplits(t *testing.T) {
	bytes := []byte("A😀B")
	for split := 0; split <= len(bytes); split++ {
		t.Run(string(rune('0'+split)), func(t *testing.T) {
			b := NewStdinBuffer(StdinBufferOptions{})
			var got []string
			if split > 0 {
				got = append(got, b.ProcessTerminalBytes(bytes[:split])...)
			}
			if split < len(bytes) {
				got = append(got, b.ProcessTerminalBytes(bytes[split:])...)
			}
			want := [][]uint16{{'A'}, {0xd83d}, {0xde00}, {'B'}}
			if len(got) != len(want) {
				t.Fatalf("events=%q want=%04x", got, want)
			}
			for i, part := range got {
				if units := jsstring.ToUTF16(part); !slices.Equal(units, want[i]) {
					t.Errorf("event%d=%04x want=%04x", i, units, want[i])
				}
			}
		})
	}
}

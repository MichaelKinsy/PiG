package tui

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Without SGR mode 1006 a terminal reports a column or row from 95 up as a byte of 0x80 or more. Pi's stdin.setEncoding("utf8")
// turns each such byte into one U+FFFD, so parseWheelEvent and isMouseSequence (tui-alt-screen.ts) see six UTF-16 units and read
// charCodeAt(3..5) as 0xFFFD. PiG counted the three-byte U+FFFD as three bytes and passed the report on as typed text.
func TestLegacyX10MouseReportCountsUTF16Units(t *testing.T) {
	for _, tc := range []struct {
		name         string
		raw          []byte
		wantX, wantY int
	}{
		{"ascii coordinates", []byte{0x1b, '[', 'M', 96, 40, 50}, 40 - 33, 50 - 33},
		{"column over 94", []byte{0x1b, '[', 'M', 96, 0x80, 50}, 0xFFFD - 33, 50 - 33},
		{"row over 94", []byte{0x1b, '[', 'M', 96, 40, 0xff}, 40 - 33, 0xFFFD - 33},
		{"both over 94", []byte{0x1b, '[', 'M', 96, 0x90, 0xa0}, 0xFFFD - 33, 0xFFFD - 33},
		{"a two-byte character is one unit", []byte{0x1b, '[', 'M', 96, 0xc3, 0xa9, 50}, 0xe9 - 33, 50 - 33},
		{"an astral character is two units", []byte{0x1b, '[', 'M', 96, 0xf0, 0x9f, 0x98, 0x80}, 0xd83d - 33, 0xde00 - 33},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := jsstring.FromUTF8(tc.raw)
			if !isMouseSequence(data) {
				t.Fatalf("isMouseSequence(%q) = false; Pi counts %d UTF-16 units", data, jsstring.Length(data))
			}
			event, ok := parseWheelEvent(data)
			if !ok {
				t.Fatalf("parseWheelEvent(%q) = not a wheel event", data)
			}
			if event.direction != -1 || event.x != tc.wantX || event.y != tc.wantY {
				t.Fatalf("parseWheelEvent(%q) = direction %d x %d y %d, want direction -1 x %d y %d", data, event.direction, event.x, event.y, tc.wantX, tc.wantY)
			}
		})
	}
	// Seven units are not an X10 report.
	if data := jsstring.FromUTF8([]byte{0x1b, '[', 'M', 96, 40, 50, 51}); isMouseSequence(data) {
		t.Fatalf("isMouseSequence(%q) = true for seven units", data)
	}
}

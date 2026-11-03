package widthx

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/tui/src/utils.ts normalizeTerminalOutput, visibleWidth

// loneSurrogateCorpus are JavaScript strings, as UTF-16 units, that hold unpaired surrogates next to text, tabs, Thai/Lao AM vowels and escape sequences.
func loneSurrogateCorpus() [][]uint16 {
	return [][]uint16{
		{0xd83d},
		{0xde00},
		{'a', 0xd83d},
		{0xd83d, 'a'},
		{0xde00, 0xd83d},
		{0xd83d, 0x0301, 'x'},
		{'a', '\t', 0xd83d, 'b'},
		{0xd83d, 0x0e33, 0xde00, 0x0eb3},
		{0x1b, '[', '3', '1', 'm', 0xd83d, 0x1b, '[', '0', 'm'},
		{0xd83d, 0x1b, '[', '7', 'm', 0xde00, 0x1b, '[', '2', '7', 'm'},
		{0xd83d, 0xde00},
		{'x', 'x', 0xd83d, 0xde00, 0xde00},
	}
}

// Pi's normalizeTerminalOutput rewrites only tabs and Thai/Lao AM vowels, and visibleWidth measures an unpaired surrogate as zero cells. Node encodes the
// surrogate when the frame is written, after the row was measured, so the string handed to the terminal keeps it.
func TestNormalizeTerminalOutputKeepsLoneSurrogatesLikePi(t *testing.T) {
	corpus := loneSurrogateCorpus()
	type result struct {
		Norm  []uint16 `json:"norm"`
		Width int      `json:"width"`
		After int      `json:"after"`
	}
	var want []result
	pioracle.Run(t, `
const u = await load("pi-tui/utils.js");
const units = (s) => Array.from({ length: s.length }, (_, i) => s.charCodeAt(i));
emit(input.map((codes) => {
	const line = String.fromCharCode(...codes);
	const norm = u.normalizeTerminalOutput(line);
	return { norm: units(norm), width: u.visibleWidth(line), after: u.visibleWidth(norm) };
}));`, corpus, &want)
	for i, codes := range corpus {
		line := jsstring.FromUTF16(codes)
		norm := NormalizeTerminalOutput(line)
		if got := jsstring.ToUTF16(norm); !slices.Equal(got, want[i].Norm) {
			t.Errorf("Normalize(%x) = %x, Pi %x", codes, got, want[i].Norm)
		}
		if got := VisibleWidth(line); got != want[i].Width {
			t.Errorf("VisibleWidth(%x) = %d, Pi %d", codes, got, want[i].Width)
		}
		if got := VisibleWidth(norm); got != want[i].After {
			t.Errorf("VisibleWidth(Normalize(%x)) = %d, Pi %d", codes, got, want[i].After)
		}
		if got, wantRow := VisibleWidth(ApplyLineReset(line)), want[i].After; got != wantRow {
			t.Errorf("VisibleWidth(ApplyLineReset(%x)) = %d, Pi %d", codes, got, wantRow)
		}
	}
}

// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package screenwriter

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/screenwriter/gridterm"
)

// keptFrameSHA256 is the hash of the synchronized update of Pi's alt-screen fixture that the writer reads and declines: step 4 of the scenario
// the fixture lists at index 485 (the tui package's screenWriterKeptFrames names it by scenario and step too).
const keptFrameSHA256 = "6b0ddf18a28c01b852865e3f7917fba5dba9a8f30a7948e0370dcd268710a03d"

// keptFrame returns a function that makes a writer that has taken every write of that scenario before the update of keptFrameSHA256, fed as
// the screen writer's oracle test feeds them, with the body of the update.
func keptFrame(t *testing.T) func() (*Writer, string) {
	t.Helper()
	file, err := os.Open("../../tui/testdata/alt-screen-oracle.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	var scenarios []struct {
		Cols, Rows int
		StartOut   string
		Log        []struct{ Out string }
	}
	if err := json.NewDecoder(zr).Decode(&scenarios); err != nil {
		t.Fatal(err)
	}
	for _, sc := range scenarios {
		writes := gridterm.SplitWrites(strings.ReplaceAll(sc.StartOut, "<terminal.start>", ""))
		for _, step := range sc.Log {
			writes = append(writes, gridterm.SplitWrites(step.Out)...)
		}
		for i, write := range writes {
			if sum := sha256.Sum256([]byte(write)); hex.EncodeToString(sum[:]) != keptFrameSHA256 {
				continue
			}
			return func() (*Writer, string) {
				w := New(Options{Size: func() (int, int) { return sc.Cols, sc.Rows }, Settle: -1})
				for _, earlier := range writes[:i] {
					w.Translate(earlier)
				}
				return w, write[len(syncBegin) : len(write)-len(syncEnd)]
			}
		}
	}
	t.Fatalf("no synchronized update of the fixture has the hash %s", keptFrameSHA256)
	return nil
}

// The writer declines the kept frame because of a pen, not because of a cursor address. The frame rewrites rows 5, 12 and 13. The line of row 5
// ends under a foreground colour it never resets, so row 12 is erased and written under that pen, and the line of row 12 ends bold: Writer.row
// has to make the terminal's pen the frame's, and the writer makes no SGR sequence but a reset (setPen). It declines at row 12 whether or not a
// cursor address follows the last row, and takes the same rows when every line ends with a reset.
func TestKeptFrameIsDeclinedForTheOpenPenOfItsRows(t *testing.T) {
	replay := keptFrame(t)
	_, body := replay()
	tail := "\x1b[?25l"
	row12, row13, end := strings.Index(body, "\x1b[12;1H"), strings.Index(body, "\x1b[13;1H"), len(body)-len(tail)
	for _, c := range []struct {
		name     string
		body     string
		accepted bool
	}{
		{"as Pi wrote it", body, false},
		{"with a cursor address after the last row", body[:end] + "\x1b[14;1H" + tail, false},
		{"row 5 alone", body[:row12] + tail, true},
		{"rows 5 and 12", body[:row13] + tail, false},
		{"every line ended by a reset", body[:row12] + "\x1b[0m" + body[row12:row13] + "\x1b[0m" + body[row13:end] + "\x1b[0m" + tail, true},
	} {
		if w, _ := replay(); w.frame(c.body, false) != c.accepted {
			t.Errorf("%s: the writer accepted it: %v, want %v", c.name, !c.accepted, c.accepted)
		}
	}
}

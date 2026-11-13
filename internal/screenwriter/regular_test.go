// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package screenwriter

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"strings"
	"testing"
)

// Regular mode, which a setting or --tui-mode regular selects, writes on the main screen and never enters the alternate one: the
// writer sends every byte of it as it came. The log is a run of the benchmark's write step (50 lines) in regular mode with the
// writer under the terminal: every write of the run and what was sent for it. The run holds synchronized updates, which in the
// alternate screen the writer would translate; here each of them, and every other write, is sent unchanged, by the writer of the
// run and by the writer now.
func TestRegularModeIsSentAsItCame(t *testing.T) {
	file, err := os.Open("testdata/regular-write50.rec.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	log, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	w := New(Options{Size: func() (int, int) { return 160, 48 }, Settle: -1})
	records, updates, size := 0, 0, 0
	for ; len(log) > 0; records++ {
		_, in, out, rest, ok := NextRecord(log)
		if !ok {
			t.Fatalf("record %d is cut short", records)
		}
		log = rest
		if strings.Contains(string(in), "\x1b[?1049h") {
			t.Fatalf("record %d enters the alternate screen: the log is not of regular mode", records)
		}
		if strings.HasPrefix(string(in), syncBegin) {
			updates++
		}
		size += len(in)
		if !bytes.Equal(out, in) {
			t.Errorf("record %d: the run sent %q for %q", records, out, in)
		}
		if got := w.Translate(string(in)); got != string(in) {
			t.Errorf("record %d: the writer sends %q for %q", records, got, in)
		}
		if w.Active() {
			t.Fatalf("record %d: the writer sees an alternate screen", records)
		}
	}
	if stats := w.Stats(); stats.Frames != 0 || w.Settled() != "" {
		t.Errorf("the writer read %d frames and has a settle write of %d bytes", stats.Frames, len(w.Settled()))
	}
	if updates < 50 || size < 10000 {
		t.Errorf("the log has %d synchronized updates in %d bytes of %d writes: too little to show that regular mode's frames pass unchanged", updates, size, records)
	}
}

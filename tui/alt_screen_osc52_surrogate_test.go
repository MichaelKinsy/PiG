package tui

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The alt screen's own OSC 52 copy (no CopySelection installed) sends Buffer.from(text).toString("base64") in Pi: the UTF-8 of the selected text, where a lone
// UTF-16 surrogate is U+FFFD (EF BF BD), never the three WTF-8 bytes PiG strings carry it as.
func TestAltScreenOSC52CopyEncodesALoneSurrogate(t *testing.T) {
	var out bytes.Buffer
	ui := newAltScreenForTest(&out, 40, 8, TuiAltScreenOptions{})
	row := "ab" + jsstring.FromUTF16([]uint16{0xd83d}) + "cd"
	ui.Add(&oracleLines{lines: []string{row, row, row, row}})
	ui.Start()
	out.Reset()
	ui.HandleViewportInput("\x1b[<0;1;2M")
	ui.HandleViewportInput("\x1b[<32;9;3M")
	ui.HandleViewportInput("\x1b[<0;9;3m")
	s := out.String()
	_, after, ok := strings.Cut(s, "\x1b]52;c;")
	if !ok {
		t.Fatalf("no OSC 52 in %q", s)
	}
	payload := after
	payload = payload[:strings.IndexByte(payload, 7)]
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(decoded) || !bytes.Contains(decoded, []byte("ab\ufffdcd")) {
		t.Fatalf("copied bytes %x (%q), want valid UTF-8 holding ab, U+FFFD, cd", decoded, decoded)
	}
}

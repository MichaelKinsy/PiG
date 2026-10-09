package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

type stdinOracleOp struct {
	Chunk *string `json:"chunk"`
	Flush bool    `json:"flush"`
	Wait  bool    `json:"wait"`
	Clear bool    `json:"clear"`
}

type stdinOracleEntry struct {
	Events  [][2]string `json:"events"`
	Flushed []string    `json:"flushed"`
	Buffer  string      `json:"buffer"`
}

// The fixture is Pi 1.0.4's StdinBuffer run over seeded random chunk sequences (testdata/stdin-buffer-oracle.mjs): partial
// CSI/OSC/DCS/APC/SS3 and old-style mouse sequences, bracketed paste split at every boundary, Kitty printable de-duplication,
// timer flushes, public flush() and clear(). Go frames a paste as one payload, so a framed event is Pi's "paste" event.
// markLoneSurrogates spells each lone UTF-16 surrogate of a WTF-8 string as the fixture does.
func markLoneSurrogates(text string) string {
	units := jsstring.ToUTF16(text)
	var out strings.Builder
	for index := 0; index < len(units); index++ {
		unit := units[index]
		switch {
		case unit >= 0xd800 && unit <= 0xdbff && index+1 < len(units) && units[index+1] >= 0xdc00 && units[index+1] <= 0xdfff:
			out.WriteString(string(utf16.DecodeRune(rune(unit), rune(units[index+1]))))
			index++
		case unit >= 0xd800 && unit <= 0xdfff:
			fmt.Fprintf(&out, "\uf8ff%x", unit)
		default:
			out.WriteRune(rune(unit))
		}
	}
	return out.String()
}

func TestStdinBufferMatchesPiOracle(t *testing.T) {
	data, err := os.ReadFile("testdata/stdin-buffer-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Options struct{ Timeout, EscapeTimeout int }
		Ops     []stdinOracleOp
		Log     []stdinOracleEntry
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for index, c := range cases {
		buffer := NewStdinBuffer(StdinBufferOptions{Timeout: time.Duration(c.Options.Timeout) * time.Millisecond, EscapeTimeout: time.Duration(c.Options.EscapeTimeout) * time.Millisecond})
		for step, op := range c.Ops {
			var emitted []string
			var flushed []string
			switch {
			case op.Chunk != nil:
				emitted = buffer.ProcessString(jsstring.FromUTF8([]byte(*op.Chunk)))
			case op.Flush:
				flushed = buffer.Flush()
			case op.Wait:
				if buffer.HasPendingFlush() {
					emitted = buffer.Flush()
				}
			case op.Clear:
				buffer.Clear()
			}
			var events [][2]string
			for _, sequence := range emitted {
				sequence = markLoneSurrogates(sequence)
				if payload, ok := strings.CutPrefix(sequence, bracketedPasteStart); ok && strings.HasSuffix(payload, bracketedPasteEnd) {
					events = append(events, [2]string{"paste", strings.TrimSuffix(payload, bracketedPasteEnd)})
					continue
				}
				events = append(events, [2]string{"data", sequence})
			}
			for i := range flushed {
				flushed[i] = markLoneSurrogates(flushed[i])
			}
			want := c.Log[step]
			if !reflect.DeepEqual(events, want.Events) && (len(events) != 0 || len(want.Events) != 0) ||
				!reflect.DeepEqual(flushed, want.Flushed) && (len(flushed) != 0 || len(want.Flushed) != 0) || markLoneSurrogates(buffer.GetBuffer()) != want.Buffer {
				failures++
				if failures <= 8 {
					t.Errorf("case %d step %d ops %+v\n got events %q flushed %q buffer %q\nwant events %q flushed %q buffer %q", index, step, c.Ops[:step+1], events, flushed, buffer.GetBuffer(), want.Events, want.Flushed, want.Buffer)
				}
				break
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d sequences differ from Pi", failures, len(cases))
	}
}

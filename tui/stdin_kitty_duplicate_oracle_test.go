package tui

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// StdinBuffer drops the raw character that a terminal repeats after a Kitty CSI-u press (parseUnmodifiedKittyPrintableCodepoint, stdin-buffer.ts).
// Only ESC [ <digits> [: <digits>*] [: <digits>+] u counts: a sign, a modifier after ';', extra fields and an oversized codepoint do not. Each probe feeds the sequence and then one raw character and records what the second call emits.
func TestStdinBufferKittyDuplicateSuppressionMatchesPi(t *testing.T) {
	type probe struct{ Sequence, Raw string }
	var probes []probe
	for _, sequence := range []string{
		"\x1b[97u", "\x1b[97:65u", "\x1b[97:u", "\x1b[97::97u", "\x1b[97:65:97u", "\x1b[97:65:u", "\x1b[97::u", "\x1b[97:65:97:1u", "\x1b[97;2u", "\x1b[97:65;2u", "\x1b[97:65:1;2u", "\x1b[97:65:;2u", "\x1b[97:65:1;;u",
		"\x1b[+97u", "\x1b[-97u", "\x1b[97:+5u", "\x1b[97:-5u", "\x1b[97:5:+5u", "\x1b[97:5:-5u", "\x1b[097u", "\x1b[0097:1:2u", "\x1b[31u", "\x1b[32u", "\x1b[0u", "\x1b[ 97u", "\x1b[97 u", "\x1b[٩٧u",
		"\x1b[99999999999999999999u", "\x1b[97:99999999999999999999u", "\x1b[97:5:99999999999999999999u", "\x1b[1114112u", "\x1b[1114111u", "\x1b[27u", "\x1b[9u",
		"\x1b[97:5:5;5u", "\x1b[97:5:5:5u", "\x1b[u", "\x1b[:u", "\x1b[97:::u",
	} {
		for _, raw := range []string{"a", "A", "\x00", "b", " ", "\t"} {
			probes = append(probes, probe{sequence, raw})
		}
	}
	var want [][]string
	pioracle.Run(t, `
const { StdinBuffer } = await load("pi-tui/stdin-buffer.js");
emit(input.map(({ Sequence, Raw }) => {
	const buffer = new StdinBuffer({ timeout: 10 });
	const events = [];
	buffer.on("data", (d) => events.push(d));
	buffer.process(Sequence);
	const first = events.length;
	buffer.process(Raw);
	const out = events.slice(first);
	buffer.destroy();
	return out;
}));`, probes, &want)
	bad := 0
	for i, p := range probes {
		b := NewStdinBuffer(StdinBufferOptions{})
		b.ProcessString(p.Sequence)
		got := b.ProcessString(p.Raw)
		if got == nil {
			got = []string{}
		}
		if len(want[i]) == 0 && len(got) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want[i]) {
			if bad++; bad <= 10 {
				t.Errorf("%q then %q: Go %q, Pi %q", p.Sequence, p.Raw, got, want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("%d of %d differ", bad, len(probes))
	}
}

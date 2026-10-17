package tui

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/tui/src/terminal-colors.ts

// parseOscColorResponse and parseTerminalColorSchemeReport over replies to OSC 4/10/11 and CSI ?997n queries: BEL and ST terminators, palette indexes of one to four
// digits, #rrggbb and #rrrrggggbbbb colors, rgb: and rgba: forms (in any case) with one to five hex digits per channel, extra channels, JS whitespace around the value
// (U+FEFF, U+00A0 trimmed; U+0085 not), non-hex digits (including non-ASCII ones Number-like parsers read), and truncated or doubled sequences.
func TestParseTerminalColorRepliesMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(263))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	hex := func(n int) string {
		out := make([]byte, n)
		for i := range out {
			out[i] = "0123456789abcdefABCDEF"[rng.Intn(22)]
		}
		return string(out)
	}
	channel := func() string {
		if rng.Intn(12) == 0 {
			return pick("", "g0", "xx", "١٢", "0x1", "-1", "1 ", " 1", "ffffff", "fffff", "+f", "1e", "٣")
		}
		return hex(1 + rng.Intn(4))
	}
	value := func() string {
		switch rng.Intn(6) {
		case 0:
			return "#" + hex(6)
		case 1:
			return "#" + hex(12)
		case 2:
			return "#" + hex([]int{3, 4, 5, 7, 8, 9, 10, 11, 13}[rng.Intn(9)])
		case 3:
			return pick("rgb:", "RGB:", "rgba:", "RgBa:", "rgb", "xrgb:", "") + channel() + "/" + channel() + "/" + channel() + pick("", "", "/"+channel(), "/", "/zz")
		case 4:
			return pick("rgb:", "") + channel() + "/" + channel()
		default:
			return pick("", " ", "red", "#", "#zzzzzz", "rgb:/", "rgb:///", "#"+hex(6)+"00")
		}
	}
	var inputs []string
	for range 5000 {
		v := value()
		if rng.Intn(6) == 0 {
			pad := pick(" ", "\t", "\n", "\u00a0", "\ufeff", "\u2028", "\u0085", "\u200b", "\v", "\x00")
			v = pad + v + pad
		}
		target := pick("10", "11", "10", "11", "4;3", "4;9", "4;0", "4;7", "4;15", "4;255", "4;256", "4;1234", "4;", "4;a", "12", "9", "4;00", "4;٣", "4", "1", "110")
		terminator := pick("\x07", "\x07", "\x07", "\x1b\\", "\x1b\\", "", "\x1b", "\\", "\x07\x07", "\x1b\\\x1b\\", "\x07 ")
		prefix := pick("\x1b]", "\x1b]", "\x1b]", "\x1b]", "\x1b]", "\x1b]", "]", "\x1b[", "\x9d", "")
		inputs = append(inputs, prefix+target+";"+v+terminator)
	}
	for range 200 {
		inputs = append(inputs, strings.Repeat("\x1b[?997;"+pick("1", "2", "3", "")+"n", rng.Intn(4)), "\x1b[?997;1n"+pick("", "x", "\n"), pick("", " ")+"\x1b[?997;2n")
	}
	type result struct {
		Osc *struct {
			Target any `json:"target"`
			RGB    any `json:"rgb"`
		} `json:"osc"`
		Scheme string `json:"scheme"`
	}
	var want []result
	pioracle.Run(t, `
const mod = await load("pi-tui/terminal-colors.js");
emit(input.map((data) => ({ osc: mod.parseOscColorResponse(data) ?? null, scheme: mod.parseTerminalColorSchemeReport(data) ?? "" })));`, inputs, &want)
	failures, parsed, colored := 0, 0, 0
	for i, data := range inputs {
		reply, ok := ParseOscColorResponse(data)
		scheme := string(ParseTerminalColorSchemeReport(data))
		w := want[i]
		var gotTarget any
		var gotRGB any
		if ok {
			parsed++
			switch reply.Target {
			case OscColorTargetForeground:
				gotTarget = "foreground"
			case OscColorTargetBackground:
				gotTarget = "background"
			default:
				gotTarget = float64(reply.Target)
			}
			if reply.RGB != nil {
				colored++
				gotRGB = map[string]any{"r": float64(reply.RGB.R), "g": float64(reply.RGB.G), "b": float64(reply.RGB.B)}
			}
		}
		wantOK := w.Osc != nil
		if ok != wantOK || scheme != w.Scheme || (ok && (fmt.Sprint(gotTarget) != fmt.Sprint(w.Osc.Target) || fmt.Sprint(gotRGB) != fmt.Sprint(w.Osc.RGB))) {
			if failures++; failures <= 10 {
				t.Errorf("%q: PiG ok=%v %v %v scheme=%q, Pi %+v scheme=%q", data, ok, gotTarget, gotRGB, scheme, w.Osc, w.Scheme)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d inputs differ", failures, len(inputs))
	}
	if parsed < 500 || colored < 250 {
		t.Fatalf("%d replies parsed, %d with a color", parsed, colored)
	}
}

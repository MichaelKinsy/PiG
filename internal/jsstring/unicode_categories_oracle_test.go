package jsstring

import (
	"testing"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Go's unicode category predicates against the property escapes of Node's regular expressions (\p{L} and so on), over every code point. PiG's ports of
// Pi regexes that use \p{..} (marked's punctuation classes, word boundaries) call these predicates, so a Unicode version gap between Go's tables and
// Node's ICU shows here.
func TestUnicodeCategoriesMatchNode(t *testing.T) {
	type flags struct {
		Cp                                            int
		L, Lu, Ll, P, S, M, N, Nd, Cc, Zs, Lt, Lm, Lo bool
	}
	var want []flags
	pioracle.Run(t, `
const props = { L: /\p{L}/u, Lu: /\p{Lu}/u, Ll: /\p{Ll}/u, P: /\p{P}/u, S: /\p{S}/u, M: /\p{M}/u, N: /\p{N}/u, Nd: /\p{Nd}/u, Cc: /\p{Cc}/u, Zs: /\p{Zs}/u, Lt: /\p{Lt}/u, Lm: /\p{Lm}/u, Lo: /\p{Lo}/u };
const out = [];
for (let cp = 0; cp <= 0x10FFFF; cp++) {
	if (cp >= 0xD800 && cp <= 0xDFFF) continue;
	const s = String.fromCodePoint(cp);
	const f = { Cp: cp };
	let any = false;
	for (const [name, re] of Object.entries(props)) { f[name] = re.test(s); any ||= f[name]; }
	if (any) out.push(f);
}
emit(out);
`, nil, &want)
	if len(want) < 100000 {
		t.Fatalf("Node reported %d code points with a category of the set", len(want))
	}
	byCp := make(map[int]flags, len(want))
	for _, f := range want {
		byCp[f.Cp] = f
	}
	bad := 0
	for cp := 0; cp <= 0x10FFFF; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF {
			continue
		}
		r := rune(cp)
		got := flags{Cp: cp,
			L: unicode.Is(unicode.L, r), Lu: unicode.Is(unicode.Lu, r), Ll: unicode.Is(unicode.Ll, r), P: unicode.Is(unicode.P, r), S: unicode.Is(unicode.S, r),
			M: unicode.Is(unicode.M, r), N: unicode.Is(unicode.N, r), Nd: unicode.Is(unicode.Nd, r), Cc: unicode.Is(unicode.Cc, r), Zs: unicode.Is(unicode.Zs, r),
			Lt: unicode.Is(unicode.Lt, r), Lm: unicode.Is(unicode.Lm, r), Lo: unicode.Is(unicode.Lo, r)}
		w, ok := byCp[cp]
		if !ok {
			w = flags{Cp: cp}
		}
		if got != w {
			if bad++; bad <= 5 {
				t.Errorf("U+%04X: Go %+v, Node %+v", cp, got, w)
			}
		}
	}
	if bad > 5 {
		t.Errorf("%d code points differ", bad)
	}
}

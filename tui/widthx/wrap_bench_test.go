package widthx

import (
	"strings"
	"testing"
)

func wrapBenchParagraph(words int) string {
	vocabulary := strings.Fields("alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega the of and to in is that for it with as was on be at by this had not are but from or have")
	var b strings.Builder
	for i := range words {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(vocabulary[(i*7+i/3)%len(vocabulary)])
	}
	return b.String()
}

// BenchmarkWrapTextWithAnsi wraps the paragraphs a transcript holds: plain prose, a thinking run styled as a whole, and prose with
// inline styles, each of a few hundred words at a 198-column width.
func BenchmarkWrapTextWithAnsi(b *testing.B) {
	plain := wrapBenchParagraph(436)
	styled := "\x1b[3m\x1b[90m" + plain + "\x1b[39m\x1b[23m"
	var inline strings.Builder
	for i, word := range strings.Fields(plain) {
		if i > 0 {
			inline.WriteByte(' ')
		}
		switch i % 9 {
		case 3:
			inline.WriteString("\x1b[1m" + word + "\x1b[22m")
		case 6:
			inline.WriteString("\x1b[36m" + word + "\x1b[39m")
		default:
			inline.WriteString(word)
		}
	}
	for _, c := range []struct{ name, text string }{{"plain", plain}, {"styled", styled}, {"inline", inline.String()}} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				WrapTextWithAnsi(c.text, 198)
			}
		})
	}
}

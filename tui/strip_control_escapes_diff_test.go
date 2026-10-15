package tui

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// stripControlEscapesByByte is the byte-at-a-time form stripControlEscapes replaced; the run-copying version must agree with it on every input.
func stripControlEscapesByByte(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	i := 0
	for i < len(s) {
		c := s[i]
		if c == 0x1b && i+1 < len(s) {
			switch s[i+1] {
			case '[':
				j := i + 2
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
				if j < len(s) {
					if s[j] == 'm' {
						out.WriteString(s[i : j+1])
					}
					i = j + 1
					continue
				}
				return out.String()
			case ']':
				j := i + 2
				for j < len(s) {
					if s[j] == 0x07 {
						j++
						break
					}
					if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				i = j
				continue
			default:
				i += 2
				continue
			}
		}
		if c == 0x1b {
			i++
			continue
		}
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
			i++
			continue
		}
		out.WriteByte(c)
		i++
	}
	return out.String()
}

func stripControlEscapesPieces() []string {
	return []string{"plain", " ", "ünï", "日本", "\t", "\n", "\r", "\r\n", "\x07", "\x08", "\x00", "\x7f", "\x1b", "\x1b[", "\x1b[31m", "\x1b[0m", "\x1b[2K", "\x1b[?25l", "\x1b[1;2H", "\x1b]0;title\x07", "\x1b]8;;http://x\x1b\\", "\x1b]", "\x1b=", "\x1b>", "\x1bc", "\xff", "\xe2\x82", "[", "]", "\\", "m"}
}

func TestStripControlEscapesMatchesByteLoop(t *testing.T) {
	pieces := stripControlEscapesPieces()
	rng := rand.New(rand.NewPCG(7, 11))
	for range 200000 {
		var b strings.Builder
		for range rng.IntN(12) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		in := b.String()
		if got, want := stripControlEscapes(in), stripControlEscapesByByte(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}

func BenchmarkStripControlEscapes(b *testing.B) {
	clean := strings.Repeat("src/main.go:42: some ordinary output line\n", 1200)
	colored := strings.Repeat("\x1b[32msrc/main.go\x1b[0m:42: some ordinary output line\x1b[K\n", 1000)
	for _, c := range []struct{ name, text string }{{"clean", clean}, {"sgr", colored}} {
		b.Run(c.name, func(b *testing.B) {
			b.SetBytes(int64(len(c.text)))
			b.ReportAllocs()
			for b.Loop() {
				_ = stripControlEscapes(c.text)
			}
		})
	}
}

// Output with nothing to strip comes back as the same string, without a copy: bash output is stripped on every render of a tool card.
func TestStripControlEscapesCleanTextIsNotCopied(t *testing.T) {
	text := strings.Repeat("src/main.go:42: ünï 日本 output\t line\r\n", 500)
	if got := testing.AllocsPerRun(20, func() { _ = stripControlEscapes(text) }); got != 0 {
		t.Fatalf("a clean 20 KB output allocates %v times per strip, want 0", got)
	}
}

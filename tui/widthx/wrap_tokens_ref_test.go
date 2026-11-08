package widthx

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// splitIntoTokensWithAnsiRef is the grapheme-by-grapheme tokenizer splitIntoTokensWithAnsi replaced: the tokens it builds string by
// string are the slices of text the zero-copy one cuts.
func splitIntoTokensWithAnsiRef(text string) []string {
	var tokens []string
	var current strings.Builder
	pendingAnsi := ""
	currentKind := byte(0)
	flushCurrent := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
		currentKind = 0
	}

	for i := 0; i < len(text); {
		if n := ExtractAnsi(text, i); n > 0 {
			pendingAnsi += text[i : i+n]
			i += n
			continue
		}

		end := i
		for end < len(text) && ExtractAnsi(text, end) == 0 {
			end++
		}
		graphemes := newGraphemeIter(text[i:end])
		for graphemes.Next() {
			segment := graphemes.Str()
			isSpace := segment == " "
			if !isSpace && isCJKBreak(segment) {
				flushCurrent()
				tokens = append(tokens, pendingAnsi+segment)
				pendingAnsi = ""
				continue
			}
			kind := byte(2)
			if isSpace {
				kind = 1
			}
			if current.Len() > 0 && currentKind != kind {
				flushCurrent()
			}
			if pendingAnsi != "" {
				current.WriteString(pendingAnsi)
				pendingAnsi = ""
			}
			currentKind = kind
			current.WriteString(segment)
		}
		i = end
	}
	if pendingAnsi != "" {
		switch {
		case current.Len() > 0:
			current.WriteString(pendingAnsi)
		case len(tokens) > 0:
			tokens[len(tokens)-1] += pendingAnsi
		default:
			current.WriteString(pendingAnsi)
		}
	}
	flushCurrent()
	return tokens
}

var tokenizerPieces = []string{
	"a", "b", "word", "Word", " ", "  ", "\t", "\x1b[1m", "\x1b[22m", "\x1b[38;5;12m", "\x1b[0m", "\x1b]8;;http://x\x07", "\x1b]8;;\x1b\\",
	"\x1b", "\x1b[", "\x1b]", "\x1b[1", "\r", "\n", "\r\n", "é", "e\u0301", " \u0301", "日", "本語", "、", "😀", "👨\u200d👩\u200d👧", "🇯🇵", "\u00a0", "\u200b",
	"\x00", "\x7f", "~", "!", "[", "m",
}

func checkTokenizer(t testing.TB, s string) {
	t.Helper()
	got, want := splitIntoTokensWithAnsi(s), splitIntoTokensWithAnsiRef(s)
	if !reflect.DeepEqual(got, want) && (len(got) != 0 || len(want) != 0) {
		t.Fatalf("splitIntoTokensWithAnsi(%+q) = %+q, want %+q", s, got, want)
	}
}

func TestSplitIntoTokensWithAnsiMatchesGraphemeTokenizer(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for range 200000 {
		var b strings.Builder
		for range rng.Intn(12) {
			b.WriteString(tokenizerPieces[rng.Intn(len(tokenizerPieces))])
		}
		checkTokenizer(t, b.String())
	}
	for _, piece := range tokenizerPieces {
		for _, other := range tokenizerPieces {
			checkTokenizer(t, piece+other)
			checkTokenizer(t, other+piece+other)
		}
	}
}

func FuzzSplitIntoTokensWithAnsi(f *testing.F) {
	for _, piece := range tokenizerPieces {
		f.Add("a " + piece + " b" + piece)
	}
	f.Fuzz(func(t *testing.T, s string) { checkTokenizer(t, s) })
}

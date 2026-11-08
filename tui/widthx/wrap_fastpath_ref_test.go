package widthx

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// updateFromTextRef is UpdateFromText before it jumped to the next ESC: it asks ExtractAnsi at every byte.
func updateFromTextRef(t *AnsiCodeTracker, text string) {
	for i := 0; i < len(text); {
		n := ExtractAnsi(text, i)
		if n == 0 {
			i++
			continue
		}
		t.Process(text[i : i+n])
		i += n
	}
}

// wrapSingleLineRef is wrapSingleLine before the printable-ASCII fast path: every token gets its width and whitespace-ness from
// VisibleWidth and isWhitespaceToken, and every token goes through the tracker.
func wrapSingleLineRef(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	if VisibleWidth(line) <= width {
		return []string{line}
	}

	var wrapped []string
	tr := &AnsiCodeTracker{}
	tokens := splitIntoTokensWithAnsi(line)

	var current []byte
	currentVisible := 0

	for _, token := range tokens {
		tokenWidth := VisibleWidth(token)
		isWhitespace := isWhitespaceToken(token)

		if tokenWidth > width && !isWhitespace {
			if len(current) > 0 {
				line := JSTrimEnd(string(current))
				if r := tr.LineEndReset(); r != "" {
					line += r
				}
				wrapped = append(wrapped, line)
			}
			broken := breakLongWord(token, width, tr)
			if len(broken) > 1 {
				wrapped = append(wrapped, broken[:len(broken)-1]...)
			}
			current = append(current[:0], broken[len(broken)-1]...)
			currentVisible = VisibleWidth(broken[len(broken)-1])
			continue
		}

		switch {
		case currentVisible+tokenWidth > width && currentVisible > 0:
			toWrap := JSTrimEnd(string(current))
			if isWhitespace {
				toWrap += extractAnsiCodes(token)
				updateFromTextRef(tr, token)
			}
			if r := tr.LineEndReset(); r != "" {
				toWrap += r
			}
			wrapped = append(wrapped, toWrap)
			if isWhitespace {
				current = append(current[:0], tr.ActiveCodes()...)
				currentVisible = 0
				continue
			}
			current = append(current[:0], tr.ActiveCodes()...)
			current = append(current, token...)
			currentVisible = tokenWidth
		case isWhitespace && currentVisible+tokenWidth > width && strings.Contains(token, "\x1b"):
			current = append(current, extractAnsiCodes(token)...)
		default:
			current = append(current, token...)
			currentVisible += tokenWidth
		}

		updateFromTextRef(tr, token)
	}

	if len(current) > 0 {
		wrapped = append(wrapped, string(current))
	}
	if len(wrapped) == 0 {
		return []string{""}
	}
	for i := range wrapped {
		wrapped[i] = JSTrimEnd(wrapped[i])
	}
	return wrapped
}

func checkWrapSingleLine(t testing.TB, s string, width int) {
	t.Helper()
	got, want := wrapSingleLine(s, width), wrapSingleLineRef(s, width)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wrapSingleLine(%+q, %d) = %+q, want %+q", s, width, got, want)
	}
}

func checkUpdateFromText(t testing.TB, s string) {
	t.Helper()
	var got, want AnsiCodeTracker
	got.UpdateFromText(s)
	updateFromTextRef(&want, s)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UpdateFromText(%+q) = %+v, want %+v", s, got, want)
	}
}

func TestWrapSingleLineMatchesUnshortcutWrap(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for range 150000 {
		var b strings.Builder
		for range rng.Intn(14) {
			b.WriteString(tokenizerPieces[rng.Intn(len(tokenizerPieces))])
		}
		s := b.String()
		for _, width := range []int{1, 2, 3, 5, 8} {
			checkWrapSingleLine(t, s, width)
		}
	}
}

func TestUpdateFromTextMatchesByteStepping(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	for range 200000 {
		var b strings.Builder
		for range rng.Intn(14) {
			b.WriteString(tokenizerPieces[rng.Intn(len(tokenizerPieces))])
		}
		checkUpdateFromText(t, b.String())
	}
	for _, piece := range tokenizerPieces {
		for _, other := range tokenizerPieces {
			checkUpdateFromText(t, piece+other)
			checkUpdateFromText(t, other+piece+other)
		}
	}
}

func FuzzWrapSingleLineFastPath(f *testing.F) {
	for _, piece := range tokenizerPieces {
		f.Add("a "+piece+" b"+piece+" cc", 3)
	}
	f.Fuzz(func(t *testing.T, s string, width int) {
		if width < 1 || width > 40 {
			return
		}
		checkWrapSingleLine(t, s, width)
		checkUpdateFromText(t, s)
	})
}

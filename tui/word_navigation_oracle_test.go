package tui

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/tui/src/word-navigation.ts

type wordNavProbe struct {
	Text    string `json:"text"`
	Cursors []int  `json:"cursors"`
}

type wordNavResult struct {
	Back []int `json:"back"`
	Fwd  []int `json:"fwd"`
}

// wordNavPieces are the building blocks of the seeded texts: ASCII words, every kind of ASCII punctuation Pi's PUNCTUATION_REGEX splits a word on,
// runs of blanks, digits, underscores, CJK, kana, Hangul, Thai, accented and combining letters, emoji and an emoji ZWJ sequence.
var wordNavPieces = []string{
	"hello", "world", "foo", "bar", "path", "x", "a1b2", "snake_case", "CamelCase", "12345", "3.14", "e-mail", "don't",
	" ", "  ", "\t", "   ", "\u00a0", ".", "..", "...", ":", "/", "\\", "-", "--", "_", "(", ")", "[", "]", "{", "}", ",", ";", "!", "?", "@", "#", "$", "%", "^", "&", "*", "+", "=", "|", "~", "`", "'", "\"", "<", ">",
	"你好", "世界", "你好世界", "こんにちは", "カタカナ", "한국어", "ภาษาไทย", "café", "naïve", "e\u0301", "Ünïcödé", "😀", "🙂🙂", "👨\u200d👩\u200d👧", "🇯🇵", "a\u200db",
}

func wordNavProbes() []wordNavProbe {
	random := rand.New(rand.NewSource(20260926))
	probes := []wordNavProbe{{Text: "", Cursors: []int{0}}}
	for _, text := range []string{"hello world", "foo.bar", "foo:bar", "path/to/file", "你好世界 test", "  hello  ", "foo...bar"} {
		probes = append(probes, wordNavProbe{Text: text})
	}
	for range 400 {
		var b strings.Builder
		for range 1 + random.Intn(7) {
			b.WriteString(wordNavPieces[random.Intn(len(wordNavPieces))])
		}
		probes = append(probes, wordNavProbe{Text: b.String()})
	}
	for i := range probes {
		if probes[i].Cursors != nil {
			continue
		}
		// Every cursor on a code point boundary, in UTF-16 code units as Pi counts them.
		units := 0
		probes[i].Cursors = append(probes[i].Cursors, 0)
		for _, r := range probes[i].Text {
			units += len(utf16.Encode([]rune{r}))
			probes[i].Cursors = append(probes[i].Cursors, units)
		}
	}
	return probes
}

// word-navigation.ts findWordBackward and findWordForward against the pinned pi-tui: the same cursor on the same text lands on the same offset,
// over seeded texts that mix word, punctuation, blank, CJK, kana, Hangul, Thai, combining and emoji runs (Pi segments words with Intl.Segmenter).
func TestFindWordNavigationMatchesPiOnSeededTexts(t *testing.T) {
	probes := wordNavProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/word_navigation.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []wordNavResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures, checked := 0, 0
	for i, probe := range probes {
		for j, cursor := range probe.Cursors {
			checked += 2
			back, fwd := FindWordBackward(probe.Text, cursor), FindWordForward(probe.Text, cursor)
			if back != expected[i].Back[j] {
				if failures++; failures <= 8 {
					t.Errorf("FindWordBackward(%q, %d) = %d, Pi %d", probe.Text, cursor, back, expected[i].Back[j])
				}
			}
			if fwd != expected[i].Fwd[j] {
				if failures++; failures <= 8 {
					t.Errorf("FindWordForward(%q, %d) = %d, Pi %d", probe.Text, cursor, fwd, expected[i].Fwd[j])
				}
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d moves differ from Pi", failures, checked)
	}
}

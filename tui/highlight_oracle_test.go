//go:build !pig_strip_syntax_highlight

package tui

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/internal/hljs"
)

// highlightOracle is testdata from automation/gen/generate-highlight-oracle.mjs: Pi's highlighting of the corpus, replayed in the same call order.
type highlightOracle struct {
	Pi        string   `json:"pi"`
	Languages []string `json:"languages"`
	Cases     []struct {
		Stage     string `json:"stage"`
		Case      int    `json:"case"`
		Supported bool   `json:"supported"`
		HTML      string `json:"html"`
		Lines     string `json:"lines"`
		Markdown  string `json:"markdown"`
	} `json:"cases"`
	Mutations []struct {
		Lang     string `json:"lang"`
		Case     int    `json:"case"`
		Mutation int    `json:"mutation"`
		HTML     string `json:"html"`
	} `json:"mutations"`
}

type highlightCorpusCase struct {
	ID   string `json:"id"`
	Lang string `json:"lang"`
	Code string `json:"code"`
}

func readHighlightOracle(t *testing.T) ([]highlightCorpusCase, highlightOracle) {
	t.Helper()
	raw, err := os.ReadFile("internal/hljs/testdata/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus []highlightCorpusCase
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	compressed, err := os.ReadFile("internal/hljs/testdata/oracle.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	raw, err = io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var oracle highlightOracle
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	return corpus, oracle
}

func highlightDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:16]
}

// mutateHighlightSample is generate-highlight-oracle.mjs mutate over UTF-16 code units: line endings, astral and case-folding characters, line separators, lone surrogates, and a mid-text insertion.
func mutateHighlightSample(code string, mutation int) string {
	units := jsstring.ToUTF16(code)
	var out []uint16
	replace := func(from rune, to string) {
		var next []uint16
		for _, u := range units {
			if rune(u) == from {
				next = append(next, jsstring.ToUTF16(to)...)
				continue
			}
			next = append(next, u)
		}
		units = next
	}
	switch mutation {
	case 0:
	case 1:
		replace('\n', "\r\n")
	case 2:
		replace('e', "é😀")
		replace('K', "\u212A")
	case 3:
		for i, u := range units {
			if u >= 'a' && u <= 'z' {
				units[i] = u - 'a' + 'A'
			}
		}
	case 4:
		replace('a', "\u2028")
		var next []uint16
		for _, u := range units {
			if u == '.' {
				u = 0xd83d
			}
			next = append(next, u)
		}
		units = next
	default:
		middle := len(units) / 2
		out = append(out, units[:middle]...)
		out = append(out, 0x017f, 0x0130, 0x017f)
		units = append(out, units[middle:]...)
	}
	return jsstring.FromUTF16(units)
}

// TestHighlightMatchesPiOracle replays Pi's highlighting of the highlight.js 10.7.3 test inputs and the PiG cases: language support, the highlight.js HTML, highlightCode and the Markdown theme's highlightCode, before and after every language loads, then mutated samples in every language.
func TestHighlightMatchesPiOracle(t *testing.T) {
	withTrueColor(t, true)
	SetTheme("dark")
	corpus, oracle := readHighlightOracle(t)
	registry := hljs.NewRegistry()
	loaded := false
	failures := 0
	fail := func(format string, args ...any) {
		t.Helper()
		failures++
		if failures <= 20 {
			t.Errorf(format, args...)
		}
	}
	for _, expected := range oracle.Cases {
		if expected.Stage == "all" && !loaded {
			registry.LoadAllLanguages()
			loaded = true
			if got := registry.Languages(); !slices.Equal(got, oracle.Languages) {
				t.Fatalf("languages after loading all differ from Pi %s:\n got %q\nwant %q", oracle.Pi, got, oracle.Languages)
			}
		}
		c := corpus[expected.Case]
		supported := c.Lang != "" && registry.SupportsLanguage(c.Lang)
		if supported != expected.Supported {
			fail("%s %s (%s): supportsLanguage=%v, Pi %v", expected.Stage, c.ID, c.Lang, supported, expected.Supported)
			continue
		}
		if supported {
			html, err := registry.Highlight(c.Code, c.Lang, true)
			if err != nil {
				html = "THROW:" + err.Error()
			}
			if got := highlightDigest(html); got != expected.HTML {
				fail("%s %s (%s): highlight.js HTML differs from Pi %s", expected.Stage, c.ID, c.Lang, oracle.Pi)
			}
		}
		if got := highlightDigest(strings.Join(highlightCodeUncached(registry, c.Code, c.Lang, false, ActiveTheme()), "\n")); got != expected.Lines {
			fail("%s %s (%s): highlightCode differs from Pi %s", expected.Stage, c.ID, c.Lang, oracle.Pi)
		}
		if got := highlightDigest(strings.Join(highlightCodeUncached(registry, c.Code, c.Lang, true, ActiveTheme()), "\n")); got != expected.Markdown {
			fail("%s %s (%s): Markdown highlightCode differs from Pi %s", expected.Stage, c.ID, c.Lang, oracle.Pi)
		}
	}
	for _, expected := range oracle.Mutations {
		c := corpus[expected.Case]
		html, err := registry.Highlight(mutateHighlightSample(c.Code, expected.Mutation), expected.Lang, true)
		if err != nil {
			html = "THROW:" + err.Error()
		}
		if got := highlightDigest(html); got != expected.HTML {
			fail("%s as %s, mutation %d: highlight.js HTML differs from Pi %s", c.ID, expected.Lang, expected.Mutation, oracle.Pi)
		}
	}
	if failures > 0 {
		t.Fatalf("%d of %d highlights differ from Pi %s", failures, 3*len(oracle.Cases)+len(oracle.Mutations), oracle.Pi)
	}
}

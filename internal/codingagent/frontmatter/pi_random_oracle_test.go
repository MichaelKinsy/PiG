package frontmatter

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/utils/frontmatter.ts parseFrontmatter and stripFrontmatter against pinned Pi over random documents assembled from fences, every newline form, a BOM, JavaScript whitespace, short YAML headers and bodies, so the fence search, header slice and body trim see unusual shapes.
func TestParseRandomDocumentsMatchesPi(t *testing.T) {
	pieces := []string{"---", "---", "\n", "\n", "\r\n", "\r", "\ufeff", " ", "\t", "\u00a0", "\u2028", "\u200b", "\u0085", "a: 1", "name: x", "description: d", "- a", "x", "body text", "#c", "...", "-", "--", "---x", "é", "😀", ": ", "'", "[", "|", "\v", "\f"}
	rng := rand.New(rand.NewSource(20261010))
	var docs []string
	for range 6000 {
		var b strings.Builder
		if rng.Intn(3) != 0 {
			b.WriteString([]string{"---\n", "---\r\n", "\ufeff---\n", "---", "--- \n", "---\n\n"}[rng.Intn(6)])
		}
		for range rng.Intn(10) {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		docs = append(docs, b.String())
	}
	// The header starts after "---" and one UTF-16 code unit, not one byte: a two-byte U+0085 or U+2028 after the fence is dropped whole, and an astral character leaves its low surrogate.
	docs = append(docs, "---\u0085- a\n---x'", "---\u2028: a: 1-#c\n------: ", "---\u0085\n---\nbody", "---\u0085a: 1\n---\nbody", "---\U0001F600a: 1\n---\nbody", "---\U0001F600\n---\nbody", "---é\n---\nb", "---\n---\nb", "---\n\n---\nb")
	var want []struct {
		Err  string `json:"err"`
		Fm   any    `json:"fm"`
		Body string `json:"body"`
	}
	pioracle.Run(t, `
const fm = await load("pi-coding-agent/utils/frontmatter.js");
emit(input.map((d) => { try { const r = fm.parseFrontmatter(d); return { err: "", fm: r.frontmatter, body: r.body }; } catch (e) { return { err: e.message, fm: null, body: "" }; } }));`, docs, &want)
	failures := 0
	for i, content := range docs {
		doc := Parse(content)
		got, fm := "", []byte("null")
		if doc.Err != nil {
			got = doc.Err.Error()
		} else if encoded, err := jsstring.MarshalJSON(jsonSafe(doc.Value)); err == nil {
			// Decoded and encoded again as the Pi side is, so a lone surrogate reads back as U+FFFD on both.
			var round any
			_ = json.Unmarshal(encoded, &round)
			fm, _ = json.Marshal(round)
		} else {
			fm = []byte(err.Error())
		}
		wantFM, _ := json.Marshal(want[i].Fm)
		if got != want[i].Err || (got == "" && (!bytes.Equal(fm, wantFM) || doc.Body != want[i].Body)) {
			if failures++; failures <= 8 {
				t.Errorf("%q:\n  Pig err=%q fm=%s body=%q\n  Pi  err=%q fm=%s body=%q", content, got, fm, doc.Body, want[i].Err, wantFM, want[i].Body)
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d documents differ from Pi", failures)
	}
}

package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type textContractOp struct {
	Op       string `json:"op"`
	Width    int    `json:"width"`
	Text     string `json:"text,omitempty"`
	PaddingX int    `json:"paddingX"`
	On       bool   `json:"on,omitempty"`
}

type textContractProbe struct {
	Text     string           `json:"text"`
	PaddingX int              `json:"paddingX"`
	PaddingY int              `json:"paddingY"`
	Bg       bool             `json:"bg"`
	Ops      []textContractOp `json:"ops"`
}

type textContractRender struct {
	Rows    []string `json:"rows"`
	BgCalls int      `json:"bgCalls"`
}

// textContractProbes are the texts at every width around the padding clamp, and operation sequences (a cache hit at the same width, a width
// change, setText with the same and with other text, whitespace-only text, setPaddingX, setCustomBgFn, invalidate) that tell a cache hit from a
// recompute by the number of background calls.
func textContractProbes() []textContractProbe {
	render := func(width int) textContractOp { return textContractOp{Op: "render", Width: width} }
	var probes []textContractProbe
	texts := []string{
		"", " ", "\t", "\t \n ", "hello", "hello world, this is a sentence that wraps over several rows at small widths", "line one\nline two\n\nline four",
		"tab\tseparated\tcolumns\there", "日本語のテキストと한국어와中文", "👨‍👩‍👧‍👦🏳️‍🌈🇺🇸 emoji run", "\x1b[1;31mbold red that wraps across lines\x1b[0m and plain tail",
		"\x1b]8;;https://example.com/a\x1b\\link text\x1b]8;;\x1b\\ after", "averyveryverylongunbrokenwordthatmustbebrokenacrossmanyrows", "a\r\nb\rc", "\u00a0\ufeff",
	}
	for _, text := range texts {
		for _, pad := range [][2]int{{0, 0}, {1, 1}, {2, 0}, {0, 2}, {3, 1}, {5, 2}} {
			for _, width := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 11, 12, 20, 40} {
				for _, bg := range []bool{false, true} {
					probes = append(probes, textContractProbe{Text: text, PaddingX: pad[0], PaddingY: pad[1], Bg: bg, Ops: []textContractOp{render(width)}})
				}
			}
		}
	}
	const sample = "some words that wrap\n\tand a tab"
	sequences := [][]textContractOp{
		{render(12), render(12), render(12)},
		{render(12), render(7), render(12)},
		{render(12), {Op: "setText", Text: sample}, render(12)},
		{render(12), {Op: "setText", Text: "other"}, render(12), render(12)},
		{render(12), {Op: "setText", Text: "  "}, render(12), {Op: "setText", Text: sample}, render(12)},
		{render(12), {Op: "setPaddingX", PaddingX: 3}, render(12), {Op: "setPaddingX", PaddingX: 0}, render(12)},
		{render(12), {Op: "setPaddingX", PaddingX: 1}, render(12)},
		{render(12), {Op: "setBg", On: true}, render(12), render(12)},
		{render(12), {Op: "setBg", On: false}, render(12)},
		{{Op: "setBg", On: true}, render(12), {Op: "setBg", On: false}, render(12)},
		{render(12), {Op: "invalidate"}, render(12), render(12)},
		{{Op: "invalidate"}, render(12)},
		{{Op: "setText", Text: ""}, render(12), render(12), {Op: "setText", Text: sample}, render(12)},
		{render(20), render(5), render(20), render(5)},
	}
	for _, ops := range sequences {
		for _, bg := range []bool{false, true} {
			probes = append(probes, textContractProbe{Text: sample, PaddingX: 1, PaddingY: 1, Bg: bg, Ops: ops})
		}
	}
	return probes
}

// TestTextRenderContractMatchesPi runs Text through the probes of textContractProbes with the pinned pi-tui and requires every render's rows and the
// background function's call count to match: the cached frame of the same text and width, the recompute after a text, padding, background or
// width change or after invalidate, whitespace-only text rendering nothing, tabs as three spaces, horizontal padding clamped to what the width leaves,
// the vertical padding rows and the background on every row.
// Pi source: packages/tui/src/components/text.ts
func TestTextRenderContractMatchesPi(t *testing.T) {
	probes := textContractProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/text_contract.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]textContractRender
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi returned %d results for %d probes", len(expected), len(probes))
	}
	failures := 0
	for i, probe := range probes {
		bgCalls := 0
		bg := func(text string) string {
			bgCalls++
			return "<bg>" + text + "</bg>"
		}
		var component *Text
		if probe.Bg {
			component = NewPaddedText(probe.Text, probe.PaddingX, probe.PaddingY, bg)
		} else {
			component = NewPaddedText(probe.Text, probe.PaddingX, probe.PaddingY, nil)
		}
		var got []textContractRender
		for _, op := range probe.Ops {
			switch op.Op {
			case "render":
				got = append(got, textContractRender{Rows: nonNil(component.Render(op.Width)), BgCalls: bgCalls})
			case "setText":
				component.SetText(op.Text)
			case "setPaddingX":
				component.SetPaddingX(op.PaddingX)
			case "setBg":
				if op.On {
					component.SetCustomBgFn(bg)
				} else {
					component.SetCustomBgFn(nil)
				}
			case "invalidate":
				component.Invalidate()
			}
		}
		want := make([]textContractRender, len(expected[i]))
		for j, render := range expected[i] {
			want[j] = textContractRender{Rows: nonNil(render.Rows), BgCalls: render.BgCalls}
		}
		if !reflect.DeepEqual(got, want) {
			if failures++; failures <= 5 {
				p, _ := json.Marshal(probe)
				g, _ := json.Marshal(got)
				e, _ := json.Marshal(want)
				t.Errorf("%s:\n  Pig %s\n  Pi  %s", p, g, e)
			}
		}
	}
	if failures > 5 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	t.Logf("probes=%d failures=%d", len(probes), failures)
}

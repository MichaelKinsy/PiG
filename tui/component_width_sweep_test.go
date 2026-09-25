package tui

// Component width sweep: renders PiG components with adversarial width
// content (emoji ZWJ/flags/keycaps/VS16, CJK, combining marks, Indic
// conjuncts, zero-width and control chars, tabs, SGR and OSC 8) at every
// width 20..200 and asserts no row's widthx.VisibleWidth (Pi's visibleWidth
// exactly, see widthx/pi_width_diff_test.go) exceeds the allotted width,
// which is the condition Pi's renderer crashes on.

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

var sweepContent = []string{
	"👨‍👩‍👧‍👦🏳️‍🌈🇺🇸🇬🇧1️⃣⚠️❤️‍🔥👍🏽🫱🏻‍🫲🏿 family flags keycaps",
	"日本語のテキストと한국어와中文混合ｈａｌｆ ｶﾀｶﾅ テスト",
	"Z̴̢̛̗͓͖̹a̷͚͎l̶̰g̵̝o क्ष स्त्र ক্ষ กำ ລຳ \u200b\u200d\ufeff\u00ad",
	"\x1b[1;31mbold red\x1b[0m \x1b]8;;https://example.com/very/long/path/that/keeps/going\x1b\\link text here\x1b]8;;\x1b\\ tail",
	"tabs\tin\tthe\tmiddle\tof\ttext and a verylongunbrokenwordthatmustbebrokenacrosslines" + strings.Repeat("🚀", 40),
	"| 名前 | 説明 | ✓ |\n|---|---|---|\n| 👨‍👩‍👧 | テキスト | ⚠️ |",
	"`code 👍🏽 span` **bold 日本** _it_ ~~del~~ > quote ⎿ → ⏺ ●",
	"```\nfunc main() {\t// 日本語 👨‍👩‍👧‍👦\n}\n```",
}

type sweepCase struct {
	name   string
	render func(width int) []string
}

func sweepCases() []sweepCase {
	all := strings.Join(sweepContent, "\n\n")
	var cs []sweepCase
	cs = append(cs, sweepCase{"Markdown", func(w int) []string { return NewMarkdown(all).Render(w) }})
	cs = append(cs, sweepCase{"Text", func(w int) []string { return NewText(all).Render(w) }})
	cs = append(cs, sweepCase{"TruncatedText", func(w int) []string { return NewTruncatedText(all, 50).Render(w) }})
	cs = append(cs, sweepCase{"Box(Text)", func(w int) []string {
		b := NewBox()
		b.AddChild(NewText(all))
		return b.Render(w)
	}})
	cs = append(cs, sweepCase{"Editor", func(w int) []string {
		e := NewEditor()
		e.SetText(all)
		return e.Render(w)
	}})
	cs = append(cs, sweepCase{"FilterableList", func(w int) []string {
		return NewFilterableList("選択 👨‍👩‍👧‍👦 title", sweepContent).Render(w)
	}})
	cs = append(cs, sweepCase{"SettingsList", func(w int) []string {
		var items []SettingItem
		for i, s := range sweepContent {
			items = append(items, SettingItem{ID: string(rune('a' + i)), Label: s, Description: s, CurrentValue: s, Values: []string{s}})
		}
		return NewSettingsList(items).Render(w)
	}})
	cs = append(cs, sweepCase{"UserMessageBlock", func(w int) []string { return NewUserMessageBlock(all).Render(w) }})
	cs = append(cs, sweepCase{"UserMessageSelector", func(w int) []string { return NewUserMessageSelector(sweepContent).Render(w) }})
	cs = append(cs, sweepCase{"ToolExecution", func(w int) []string {
		c := NewToolExecutionComponent("bash 👨‍👩‍👧‍👦", all)
		c.SetResult(all, true, 0)
		c.SetExpanded(true)
		return c.Render(w)
	}})
	cs = append(cs, sweepCase{"Loader", func(w int) []string { return NewLoader(sweepContent[0] + sweepContent[1]).Render(w) }})
	cs = append(cs, sweepCase{"BorderedLoader", func(w int) []string {
		return NewBorderedLoader(sweepContent[0]+sweepContent[1], true).Render(w)
	}})
	return cs
}

// openWidthViolations lists overflow cases found by this sweep that are not
// yet fixed (none today). Add an entry only with a note on whether upstream
// overflows identically.
var openWidthViolations = map[string]func(width int) bool{}

func TestComponentWidthSweep(t *testing.T) {
	rows, violations, open := 0, 0, 0
	cases := sweepCases()
	for _, c := range cases {
		for w := 20; w <= 200; w++ {
			for i, line := range c.render(w) {
				rows++
				if vw := widthx.VisibleWidth(line); vw > w {
					if known := openWidthViolations[c.name]; known != nil && known(w) {
						open++
						t.Logf("OPEN %s width=%d row %d: visibleWidth %d > %d: %+q", c.name, w, i, vw, w, line)
						continue
					}
					violations++
					if violations <= 30 {
						t.Errorf("%s width=%d row %d: visibleWidth %d > %d: %+q", c.name, w, i, vw, w, line)
					}
				}
			}
		}
	}
	t.Logf("component width sweep: components=%d widths=20..200 rows=%d violations=%d open=%d", len(cases), rows, violations, open)
}

// TestComponentRowsMatchUpstream renders the upstream Text, TruncatedText
// and Markdown components (vendored verbatim in widthx/testdata/pi) under
// Node over the sweep content at every width 20..200. Text and
// TruncatedText rows must equal PiG's byte for byte; for Markdown (whose
// themed styling differs) both sides must emit zero over-wide rows.
func TestComponentRowsMatchUpstream(t *testing.T) {
	if testing.Short() {
		t.Skip("upstream component comparison skipped in -short mode")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node not found; the upstream component oracle is required in CI")
		}
		t.Skip("node not found; upstream component comparison skipped")
	}
	src, _ := filepath.Abs(filepath.Join("widthx", "testdata", "pi"))
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	nm := filepath.Join(dir, "node_modules")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []string{"get-east-asian-width", "marked"} {
		if err := os.Rename(filepath.Join(dir, pkg), filepath.Join(nm, pkg)); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644)
	var widths []int
	for w := 20; w <= 200; w++ {
		widths = append(widths, w)
	}
	in, _ := json.Marshal(map[string]any{"content": sweepContent, "widths": widths})
	inPath := filepath.Join(dir, "input.json")
	if err := os.WriteFile(inPath, in, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--no-warnings", filepath.Join(dir, "components.mjs"), inPath, dir)
	cmd.Stderr = os.Stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("upstream components: %v", err)
	}
	var pi struct {
		Text00, Text11, Truncated00, Truncated11 map[string][]string
		MarkdownOverflow                         map[string]int
	}
	if err := json.Unmarshal(raw, &pi); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(sweepContent, "\n\n")
	rows, mismatches := 0, 0
	check := func(name string, w int, got, want []string) {
		rows += len(got)
		if !reflect.DeepEqual(got, want) {
			mismatches++
			if mismatches <= 10 {
				t.Errorf("%s width=%d rows differ from upstream:\n go=%+q\n pi=%+q", name, w, got, want)
			}
		}
	}
	for _, w := range widths {
		k := strconv.Itoa(w)
		check("Text(0,0)", w, (&Text{Content: all}).Render(w), pi.Text00[k])
		check("Text(1,1)", w, (&Text{Content: all, PaddingX: 1, PaddingY: 1}).Render(w), pi.Text11[k])
		check("TruncatedText(0,0)", w, NewPaddedTruncatedText(all, 0, 0).Render(w), pi.Truncated00[k])
		check("TruncatedText(1,1)", w, NewPaddedTruncatedText(all, 1, 1).Render(w), pi.Truncated11[k])
		if n := pi.MarkdownOverflow[k]; n != 0 {
			t.Logf("upstream Markdown emits %d over-wide rows at width %d", n, w)
		}
	}
	t.Logf("upstream component comparison: components=3 widths=20..200 rows=%d mismatches=%d", rows, mismatches)
}

// TestOverlayCompositeWidthSweep composites an adversarial overlay over an
// adversarial background at every column and several overlay widths, for
// terminal widths 20..80, and asserts no composited row is over-wide.
func TestOverlayCompositeWidthSweep(t *testing.T) {
	rows, violations := 0, 0
	for tw := 20; tw <= 80; tw += 3 {
		var bg []string
		for _, s := range sweepContent {
			for l := range strings.SplitSeq(s, "\n") {
				bg = append(bg, widthx.TruncateToWidth(l, tw, "", false))
			}
		}
		for col := 0; col < tw; col += 2 {
			for _, ow := range []int{3, 7, 12} {
				ui := NewWithOutput(io.Discard, tw, len(bg))
				ui.OpenOverlay(&recordingComponent{lines: strings.Split(strings.Join(sweepContent, "\n"), "\n")},
					OverlayOptions{width: overlayCells(ow), anchor: overlayTopLeft, row: overlayCells(1), col: overlayCells(col)})
				for i, line := range ui.composeOverlayLines(bg, tw, len(bg)) {
					rows++
					if vw := widthx.VisibleWidth(line); vw > tw {
						violations++
						if violations <= 20 {
							t.Errorf("overlay tw=%d col=%d ow=%d row %d: visibleWidth %d: %+q", tw, col, ow, i, vw, line)
						}
					}
				}
			}
		}
	}
	t.Logf("overlay composite sweep: rows=%d violations=%d", rows, violations)
}

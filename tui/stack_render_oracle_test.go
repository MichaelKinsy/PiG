package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type stackOracleChild struct {
	Text            string   `json:"text,omitempty"`
	Rows            []string `json:"rows"`
	Basis           *int     `json:"basis"`
	Grow            *int     `json:"grow"`
	Shrink          *int     `json:"shrink"`
	MinSize         *int     `json:"minSize"`
	MaxSize         *int     `json:"maxSize"`
	VisibleMinWidth *int     `json:"visibleMinWidth"`
}

type stackOracleProbe struct {
	Kind      string             `json:"kind"`
	Gap       *int               `json:"gap"`
	Align     string             `json:"align"`
	Children  []stackOracleChild `json:"children"`
	Widths    []int              `json:"widths"`
	Scrollbar string             `json:"scrollbar,omitempty"`
}

type stackOracleRows struct{ rows []string }

func (c stackOracleRows) Render(int) []string { return c.rows }
func (c stackOracleRows) Invalidate()         {}

// stackOracleProbes is the fixed-seed corpus: wrapped text and fixed rows (plain, styled, wider than their column, empty) with basis, grow, shrink, min, max and
// visibility options, gaps and every alignment, at widths from 0 to 80, plus ScrollView in each scrollbar mode.
func stackOracleProbes() []stackOracleProbe {
	rng := rand.New(rand.NewSource(20261015))
	pick := func(n int) *int { v := rng.Intn(n); return &v }
	maybe := func(n int) *int {
		if rng.Intn(3) != 0 {
			return nil
		}
		return pick(n)
	}
	texts := []string{"", "a", "short", "a rather long text that wraps in narrow columns", "日本語 text 🙂", "\x1b[31mred\x1b[0m styled words that wrap around", "one\ntwo\nthree"}
	rowSets := [][]string{{}, {"x"}, {"left", "mid dle", "r"}, {"\x1b[1mbold\x1b[22m row", "plain"}, {"a very wide fixed row that exceeds a narrow column", "b"}, {"日本語", "🙂🙂"}, {"", "", "x"}}
	var probes []stackOracleProbe
	for range 700 {
		probe := stackOracleProbe{Kind: []string{"h", "v"}[rng.Intn(2)], Align: []string{"", "stretch", "start", "center", "end"}[rng.Intn(5)], Widths: []int{0, 1, 2, 5, 10, 17, 40, 80}}
		if rng.Intn(2) == 0 {
			probe.Gap = pick(4)
		}
		for range rng.Intn(5) {
			child := stackOracleChild{Basis: maybe(30), Grow: maybe(4), Shrink: maybe(3), MinSize: maybe(8), MaxSize: maybe(40)}
			if rng.Intn(2) == 0 {
				child.Text = texts[rng.Intn(len(texts))]
			} else {
				child.Rows = rowSets[rng.Intn(len(rowSets))]
			}
			if rng.Intn(5) == 0 {
				child.VisibleMinWidth = pick(30)
			}
			probe.Children = append(probe.Children, child)
		}
		probes = append(probes, probe)
	}
	for _, scrollbar := range []string{"hidden", "auto", "always"} {
		for _, text := range texts {
			probes = append(probes, stackOracleProbe{Kind: "scroll", Scrollbar: scrollbar, Children: []stackOracleChild{{Text: text}}, Widths: []int{0, 1, 2, 3, 10, 40}})
		}
		for _, rows := range rowSets {
			probes = append(probes, stackOracleProbe{Kind: "scroll", Scrollbar: scrollbar, Children: []stackOracleChild{{Rows: rows}}, Widths: []int{0, 1, 2, 3, 10, 40}})
		}
	}
	return probes
}

// renderStackProbe renders probe with Pig's stacks and ScrollView at every width of the probe.
func renderStackProbe(probe stackOracleProbe) [][]string {
	var children []StackChild
	for _, c := range probe.Children {
		entry := StackEntry{StackEntryOptions: StackEntryOptions{Basis: c.Basis, Grow: c.Grow, Shrink: c.Shrink, MinSize: c.MinSize, MaxSize: c.MaxSize}}
		if c.Rows != nil {
			entry.Component = stackOracleRows{rows: c.Rows}
		} else {
			entry.Component = NewText(c.Text)
		}
		if c.VisibleMinWidth != nil {
			minWidth := *c.VisibleMinWidth
			entry.Visible = func(viewport LayoutViewport) bool { return viewport.Width >= minWidth }
		}
		children = append(children, entry)
	}
	options := StackOptions{Gap: probe.Gap, Align: probe.Align}
	var render func(int) []string
	switch probe.Kind {
	case "h":
		render = NewHStack(children, options).Render
	case "scroll":
		render = NewScrollView(children[0].Component, ScrollViewOptions{Scrollbar: ScrollViewScrollbar(probe.Scrollbar)}).Render
	default:
		render = NewVStack(children, options).Render
	}
	out := make([][]string, len(probe.Widths))
	for i, width := range probe.Widths {
		out[i] = render(width)
		if out[i] == nil {
			out[i] = []string{}
		}
	}
	return out
}

// TestStackProbeDump prints the corpus for the Pi side of the stack-render parity scenario.
func TestStackProbeDump(t *testing.T) {
	line, err := json.Marshal(stackOracleProbes())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("stack-probes:%s\n", line)
}

// TestStackRenderParity prints Pig's renders of the corpus, one JSON line per probe, for the stack-render parity scenario.
func TestStackRenderParity(t *testing.T) {
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	for _, probe := range stackOracleProbes() {
		line, err := json.Marshal(renderStackProbe(probe))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("stack-observation:%s\n", line)
	}
}

// HStack.render, VStack.render and ScrollView.render (components/h-stack.ts, v-stack.ts, stack.ts allocateStackSizes, scroll-view.ts: the content width and the scrollbar column) against pinned pi-tui: wrapped text and fixed rows (plain,
// styled, wider than their column, empty) with basis, grow, shrink, min, max and visibility options, gaps and every alignment, at widths from 0 to 80.
func TestStackRenderMatchesPi(t *testing.T) {
	probes := stackOracleProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/stack_render.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][][]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	failures := 0
	for i, probe := range probes {
		for j, got := range renderStackProbe(probe) {
			if !reflect.DeepEqual(got, expected[i][j]) {
				if failures++; failures <= 5 {
					t.Errorf("%s stack %+v at width %d:\n  Pig %q\n  Pi  %q", probe.Kind, probe, probe.Widths[j], got, expected[i][j])
				}
			}
		}
	}
	if failures > 5 {
		t.Errorf("%d renders differ from Pi", failures)
	}
}

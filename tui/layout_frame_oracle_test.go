package tui

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type layoutOracleChild struct {
	Spec    *layoutOracleSpec `json:"spec"`
	Basis   *int              `json:"basis"`
	Grow    *int              `json:"grow"`
	Shrink  *int              `json:"shrink"`
	MinSize *int              `json:"minSize"`
	MaxSize *int              `json:"maxSize"`
}

type layoutOracleSpec struct {
	T         string              `json:"t"`
	Text      string              `json:"text,omitempty"`
	Rows      []string            `json:"rows,omitempty"`
	Children  []layoutOracleChild `json:"children,omitempty"`
	Gap       *int                `json:"gap"`
	Align     string              `json:"align,omitempty"`
	Child     *layoutOracleSpec   `json:"child,omitempty"`
	Follow    string              `json:"follow,omitempty"`
	Primary   bool                `json:"primary,omitempty"`
	Scrollbar string              `json:"scrollbar,omitempty"`
}

type layoutOracleOp struct {
	View int    `json:"view"`
	Kind string `json:"kind"`
	N    int    `json:"n"`
}

type layoutOracleProbe struct {
	Tree   *layoutOracleSpec `json:"tree"`
	Width  int               `json:"width"`
	Height int               `json:"height"`
	Ops    []layoutOracleOp  `json:"ops"`
}

type layoutOracleRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type layoutOracleBox struct {
	Rect       layoutOracleRect  `json:"rect"`
	Clip       layoutOracleRect  `json:"clip"`
	LineOffset int               `json:"lineOffset"`
	Layer      int               `json:"layer"`
	HasLines   bool              `json:"hasLines"`
	Children   []layoutOracleBox `json:"children"`
}

// layoutCountingRows is a fixed-rows leaf that records its render calls ([leaf index, width]) so the oracle observes renderCached's memoizing.
type layoutCountingRows struct {
	id    int
	rows  []string
	calls *[][2]int
}

func (c *layoutCountingRows) Render(width int) []string {
	*c.calls = append(*c.calls, [2]int{c.id, width})
	return c.rows
}
func (c *layoutCountingRows) Invalidate() {}

type layoutOracleFrame struct {
	Calls   [][2]int        `json:"calls"`
	Lines   []string        `json:"lines"`
	Root    layoutOracleBox `json:"root"`
	Scrolls [][2]any        `json:"scrolls"`
	Primary int             `json:"primary"`
}

func layoutOracleTree(rng *rand.Rand, depth int, scrolls *int) *layoutOracleSpec {
	texts := []string{"", "a", "short text", "a longer line of text that wraps in narrow areas", "日本語 text 🙂", "\x1b[31mred\x1b[0m styled words that wrap", "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten"}
	rowSets := [][]string{{}, {"x"}, {"left", "mid dle", "r"}, {"\x1b[1mbold\x1b[22m row", "plain", "third", "fourth", "fifth", "sixth"}, {"a very wide fixed row that exceeds a narrow column", "b"}, {"1", "2", "3", "4", "5\x1b_pi:c\x07x", "6"}, {"\x1b_pi:c\x07a", "b", "c"}, {"r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7 cursor\x1b_pi:c\x07"}}
	opt := func(n int) *int {
		if rng.Intn(3) != 0 {
			return nil
		}
		v := rng.Intn(n)
		return &v
	}
	if depth == 0 || rng.Intn(4) == 0 {
		if rng.Intn(2) == 0 {
			return &layoutOracleSpec{T: "text", Text: texts[rng.Intn(len(texts))]}
		}
		return &layoutOracleSpec{T: "rows", Rows: rowSets[rng.Intn(len(rowSets))]}
	}
	if rng.Intn(3) == 0 {
		*scrolls++
		return &layoutOracleSpec{T: "scroll", Child: layoutOracleTree(rng, depth-1, scrolls), Follow: []string{"none", "end"}[rng.Intn(2)], Primary: rng.Intn(3) == 0, Scrollbar: []string{"hidden", "auto", "always"}[rng.Intn(3)]}
	}
	spec := &layoutOracleSpec{T: []string{"v", "h"}[rng.Intn(2)], Align: []string{"", "stretch", "start", "center", "end"}[rng.Intn(5)]}
	if rng.Intn(2) == 0 {
		gap := rng.Intn(3)
		spec.Gap = &gap
	}
	for range 1 + rng.Intn(3) {
		spec.Children = append(spec.Children, layoutOracleChild{Spec: layoutOracleTree(rng, depth-1, scrolls), Basis: opt(12), Grow: opt(3), Shrink: opt(3), MinSize: opt(5), MaxSize: opt(30)})
	}
	return spec
}

func buildLayoutOracle(spec *layoutOracleSpec, scrolls *[]*ScrollView, calls *[][2]int, nextRows *int) Component {
	switch spec.T {
	case "text":
		return NewText(spec.Text)
	case "rows":
		id := *nextRows
		*nextRows++
		return &layoutCountingRows{id: id, rows: spec.Rows, calls: calls}
	case "scroll":
		view := NewScrollView(buildLayoutOracle(spec.Child, scrolls, calls, nextRows), ScrollViewOptions{Follow: spec.Follow, Primary: spec.Primary, Scrollbar: ScrollViewScrollbar(spec.Scrollbar)})
		*scrolls = append(*scrolls, view)
		return view
	}
	var children []StackChild
	for _, c := range spec.Children {
		children = append(children, StackEntry{Component: buildLayoutOracle(c.Spec, scrolls, calls, nextRows), StackEntryOptions: StackEntryOptions{Basis: c.Basis, Grow: c.Grow, Shrink: c.Shrink, MinSize: c.MinSize, MaxSize: c.MaxSize}})
	}
	options := StackOptions{Gap: spec.Gap, Align: spec.Align}
	if spec.T == "h" {
		return NewHStack(children, options)
	}
	return NewVStack(children, options)
}

func summarizeLayoutBox(box *LayoutBox) layoutOracleBox {
	out := layoutOracleBox{Rect: layoutOracleRect(box.Rect), Clip: layoutOracleRect(box.Clip), LineOffset: box.LineOffset, Layer: box.Layer, HasLines: box.Lines != nil, Children: []layoutOracleBox{}}
	for _, child := range box.Children {
		out.Children = append(out.Children, summarizeLayoutBox(child))
	}
	return out
}

func renderLayoutProbe(probe layoutOracleProbe) []layoutOracleFrame {
	var scrolls []*ScrollView
	calls, nextRows := [][2]int{}, 0
	component := buildLayoutOracle(probe.Tree, &scrolls, &calls, &nextRows)
	frameOf := func() layoutOracleFrame {
		calls = calls[:0]
		frame := RenderLayoutFrame(component, probe.Width, probe.Height, func() {})
		out := layoutOracleFrame{Calls: slices.Clone(calls), Lines: frame.Lines, Root: summarizeLayoutBox(frame.Root), Primary: -1, Scrolls: [][2]any{}}
		for i, view := range scrolls {
			out.Scrolls = append(out.Scrolls, [2]any{view.ScrollTop(), view.IsFollowingEnd()})
			if frame.PrimaryScrollView == view {
				out.Primary = i
			}
		}
		return out
	}
	frames := []layoutOracleFrame{frameOf()}
	for _, op := range probe.Ops {
		if op.View < len(scrolls) {
			view := scrolls[op.View]
			switch op.Kind {
			case "to":
				view.ScrollTo(op.N)
			case "by":
				view.ScrollBy(op.N)
			case "end":
				view.ScrollToEnd()
			case "start":
				view.ScrollToStart()
			}
		}
		frames = append(frames, frameOf())
	}
	return frames
}

// RenderLayoutFrame (layout.ts renderLayoutFrame, renderCached and the layout it paints) against pinned pi-tui over 600 seeded trees of text, fixed rows, nested
// stacks and scroll views (follow, primary, every scrollbar mode) at viewports from 1x1 to 80x24, before and after scrollTo, scrollBy, scrollToStart and scrollToEnd:
// the painted lines, every box's rect, clip, line offset and layer, each scroll view's position and follow state, and the primary scroll view must be equal.
func TestRenderLayoutFrameMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261017))
	var probes []layoutOracleProbe
	for range 600 {
		scrolls := 0
		probe := layoutOracleProbe{Tree: layoutOracleTree(rng, 3, &scrolls), Width: []int{1, 2, 5, 12, 30, 80}[rng.Intn(6)], Height: []int{1, 2, 4, 9, 24}[rng.Intn(5)]}
		for range rng.Intn(6) {
			probe.Ops = append(probe.Ops, layoutOracleOp{View: rng.Intn(scrolls + 1), Kind: []string{"to", "by", "end", "start"}[rng.Intn(4)], N: rng.Intn(40) - 10})
		}
		probes = append(probes, probe)
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/layout_frame.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []json.RawMessage
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	failures, scrolled := 0, 0
	for i, probe := range probes {
		got, err := json.Marshal(renderLayoutProbe(probe))
		if err != nil {
			t.Fatal(err)
		}
		var have, want any
		_ = json.Unmarshal(got, &have)
		_ = json.Unmarshal(expected[i], &want)
		if bytes.Contains(got, []byte(`"scrolls":[[`)) {
			scrolled++
		}
		if !reflect.DeepEqual(have, want) {
			if failures++; failures <= 3 {
				spec, _ := json.Marshal(probe)
				t.Errorf("%s\n  Pig %.1500s\n  Pi  %.1500s", spec, got, expected[i])
			}
		}
	}
	if failures > 3 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	if scrolled < len(probes)/4 {
		t.Errorf("only %d of %d probes contain a scroll view", scrolled, len(probes))
	}
}

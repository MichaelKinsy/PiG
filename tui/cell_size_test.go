package tui

import (
	"bytes"
	"strings"
	"testing"
)

// withImageTerminal mirrors tui-cell-size-input.test.ts withImageTerminal: it
// reports an image-capable terminal and restores the capabilities and cell
// dimensions afterwards.
func withImageTerminal(t *testing.T) {
	t.Helper()
	prevCaps := GetCapabilities()
	prevDims := GetCellDimensions()
	t.Cleanup(func() {
		SetCapabilities(prevCaps)
		SetCellDimensions(prevDims)
	})
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
}

func newCellSizeTestTUI(out *bytes.Buffer) *TUI {
	tui := NewWithOutput(out, 80, 24)
	tui.SetRenderDispatcher(func(func()) {})
	return tui
}

// Ported from tui-cell-size-input.test.ts "forwards bare escape even when a
// cell size query was sent at startup".
func TestCellSizeQueryForwardsBareEscape(t *testing.T) {
	withImageTerminal(t)
	var out bytes.Buffer
	tui := newCellSizeTestTUI(&out)

	tui.QueryCellSize()

	if !strings.Contains(out.String(), "\x1b[16t") {
		t.Fatalf("startup output %q lacks the CSI 16 t cell-size query", out.String())
	}
	if tui.ConsumeCellSizeResponse("\x1b") {
		t.Fatal("bare escape was consumed as a cell-size response")
	}
}

// Ported from tui-cell-size-input.test.ts "consumes cell size responses and
// still forwards later user input".
func TestCellSizeResponseConsumedAndLaterInputForwarded(t *testing.T) {
	withImageTerminal(t)
	SetCellDimensions(CellDimensions{WidthPx: 9, HeightPx: 18})
	tui := newCellSizeTestTUI(&bytes.Buffer{})

	if !tui.ConsumeCellSizeResponse("\x1b[6;20;10t") {
		t.Fatal("cell-size response was forwarded to the focused component")
	}
	if got, want := GetCellDimensions(), (CellDimensions{WidthPx: 10, HeightPx: 20}); got != want {
		t.Fatalf("cell dimensions = %+v, want %+v", got, want)
	}
	if tui.ConsumeCellSizeResponse("q") {
		t.Fatal("later user input was consumed")
	}
}

func TestCellSizeQuerySkippedWithoutImageSupport(t *testing.T) {
	withImageTerminal(t)
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	var out bytes.Buffer
	newCellSizeTestTUI(&out).QueryCellSize()
	if out.Len() != 0 {
		t.Fatalf("terminal without images was queried: %q", out.String())
	}
}

func TestAltScreenStartQueriesCellSizeBeforeFirstRender(t *testing.T) {
	withImageTerminal(t)
	var out bytes.Buffer
	alt := newAltScreenForTest(&out, 40, 10, TuiAltScreenOptions{})
	alt.Start()
	t.Cleanup(alt.Stop)

	output := out.String()
	queryIndex := strings.Index(output, cellSizeQuery)
	renderIndex := strings.Index(output, altBeginSynchronizedOutput)
	if queryIndex < 0 {
		t.Fatalf("startup output %q lacks the CSI 16 t cell-size query", output)
	}
	if renderIndex < 0 || queryIndex > renderIndex {
		t.Fatalf("cell-size query index %d, first render index %d in %q", queryIndex, renderIndex, output)
	}
}

// consumeCellSizeResponse consumes only an exact response, and consumes but
// ignores one that reports a zero dimension.
func TestCellSizeResponseParsing(t *testing.T) {
	withImageTerminal(t)
	SetCellDimensions(CellDimensions{WidthPx: 9, HeightPx: 18})
	tui := newCellSizeTestTUI(&bytes.Buffer{})
	for _, data := range []string{"\x1b[6;0;10t", "\x1b[6;20;0t"} {
		if !tui.ConsumeCellSizeResponse(data) {
			t.Fatalf("%q was not consumed", data)
		}
	}
	if got, want := GetCellDimensions(), (CellDimensions{WidthPx: 9, HeightPx: 18}); got != want {
		t.Fatalf("zero-dimension response changed cell dimensions to %+v", got)
	}
	for _, data := range []string{"\x1b[6;20t", "\x1b[4;20;10t", "\x1b[6;20;10tq", "x\x1b[6;20;10t", "\x1b[6;a;10t"} {
		if tui.ConsumeCellSizeResponse(data) {
			t.Fatalf("%q was consumed", data)
		}
	}
}

type invalidationRecorder struct {
	invalidatable
	invalidations int
}

func (r *invalidationRecorder) Render(int) []string { return []string{""} }
func (r *invalidationRecorder) Invalidate()         { r.invalidations++; r.invalidatable.Invalidate() }

// A cell-size change invalidates every mounted descendant and overlay so images
// re-render at the new size (upstream invalidate() over getMountedRoots and the
// overlay stack).
func TestCellSizeResponseInvalidatesMountedTree(t *testing.T) {
	withImageTerminal(t)
	nested := &invalidationRecorder{}
	overlay := &invalidationRecorder{}
	tui := newCellSizeTestTUI(&bytes.Buffer{})
	tui.Add(NewContainer(NewVStack([]StackChild{{Component: NewScrollView(NewContainer(nested), ScrollViewOptions{})}}, StackOptions{})))
	tui.OpenOverlay(overlay, OverlayOptions{})

	tui.ConsumeCellSizeResponse("\x1b[6;20;10t")

	if nested.invalidations != 1 || overlay.invalidations != 1 {
		t.Fatalf("invalidations: nested=%d overlay=%d, want 1 each", nested.invalidations, overlay.invalidations)
	}
}

func TestCellSizeResponseInvalidatesAltScreenLayoutRoot(t *testing.T) {
	withImageTerminal(t)
	var out bytes.Buffer
	alt := newAltScreenForTest(&out, 40, 10, TuiAltScreenOptions{})
	base := &invalidationRecorder{}
	inLayout := &invalidationRecorder{}
	alt.Add(base)
	alt.QueryCellSize()
	if !strings.Contains(out.String(), "\x1b[16t") {
		t.Fatalf("alt-screen output %q lacks the cell-size query", out.String())
	}

	alt.ConsumeCellSizeResponse("\x1b[6;20;10t")
	if base.invalidations != 1 {
		t.Fatalf("implicit document child invalidations = %d, want 1", base.invalidations)
	}

	alt.SetLayoutRoot(NewVStack([]StackChild{{Component: inLayout}}, StackOptions{}))
	alt.ConsumeCellSizeResponse("\x1b[6;22;11t")
	if inLayout.invalidations != 1 || base.invalidations != 1 {
		t.Fatalf("layout-root invalidations: layout=%d base=%d, want 1 and 1", inLayout.invalidations, base.invalidations)
	}
}

// An Image re-renders at the new cell size once the response arrives.
func TestCellSizeResponseReRendersImageRows(t *testing.T) {
	withImageTerminal(t)
	SetCellDimensions(CellDimensions{WidthPx: 10, HeightPx: 20})
	img := NewImage("", "image/png", ImageOptions{MaxWidthCells: 10, ImageID: 1}, &ImageDimensions{WidthPx: 100, HeightPx: 100})
	tui := newCellSizeTestTUI(&bytes.Buffer{})
	tui.Add(NewContainer(img))
	before := len(tui.RenderSnapshot(80))

	tui.ConsumeCellSizeResponse("\x1b[6;10;10t")

	if after := len(tui.RenderSnapshot(80)); after != 2*before {
		t.Fatalf("image rows after halving cell height = %d, want %d", after, 2*before)
	}
}

// QueryCellSize runs on the interactive driver while a scheduled render may
// query image capabilities. The shared capability cache must support both
// callers without a race.
func TestQueryCellSizeCapabilityCacheIsRaceSafe(t *testing.T) {
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	ResetCapabilitiesCache()
	var out bytes.Buffer
	renderer := newCellSizeTestTUI(&out)
	done := make(chan struct{})
	go func() {
		_ = kittyImagesActive()
		close(done)
	}()
	renderer.QueryCellSize()
	<-done
}

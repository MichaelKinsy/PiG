package tui

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type stoppedOracleTimer struct{}

func (stoppedOracleTimer) Stop() bool { return true }

type oracleLines struct{ lines []string }

func (c *oracleLines) Render(int) []string { return append([]string(nil), c.lines...) }
func (*oracleLines) Invalidate()           {}

// The fixture is the pinned Pi's TuiMainScreen run over seeded component trees (flat and nested containers), terminal sizes
// (including 1-row and 3-row terminals), and frame sequences with growth, shrink, in-place edits, resizes, forced renders,
// clear-on-shrink and hardware-cursor toggles, and cursor markers (testdata/main-screen-oracle.mjs). Each step compares
// the bytes written to the terminal; the components return the lines Pi's components returned at that step's size.
func TestMainScreenFramesMatchPiOracle(t *testing.T) {

	file, err := os.Open("testdata/main-screen-oracle.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	var scenarios []struct {
		Cols, Rows, Parts int
		Nested            bool
		Images            string
		Steps             []struct {
			Set            map[string][]string
			Resize         []int
			Force          bool
			ClearOnShrink  *bool
			HardwareCursor *bool
			CursorFirst    bool
			Overlay        *struct {
				Spec  oracleOverlaySpec
				Lines []string
			}
			HideOverlay bool
		}
		Log []struct {
			Out      string
			Rendered [][]string
			Overlays [][]string
			Error    string
		}
	}
	if err := json.NewDecoder(zr).Decode(&scenarios); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ResetCapabilitiesCache)
	failures := 0
	for index, sc := range scenarios {
		if sc.Images != "" {
			SetCapabilities(TerminalCapabilities{Images: ImageProtocol(sc.Images), TrueColor: true, Hyperlinks: true})
		} else {
			ResetCapabilitiesCache()
		}
		var out strings.Builder
		ui := NewWithOutput(&out, sc.Cols, sc.Rows)
		var pending func()
		ui.afterFunc = func(_ time.Duration, fn func()) stoppableTimer {
			pending = fn
			return stoppedOracleTimer{}
		}
		parts := make([]*oracleLines, sc.Parts)
		for i := range parts {
			parts[i] = &oracleLines{}
		}
		if sc.Nested && sc.Parts > 1 {
			inner := NewContainer()
			for _, part := range parts[1:] {
				inner.Add(part)
			}
			ui.Add(parts[0])
			ui.Add(inner)
		} else {
			for _, part := range parts {
				ui.Add(part)
			}
		}
		var overlays []*oracleLines
		var handles []*OverlayHandle
		for step, spec := range sc.Steps {
			out.Reset()
			if spec.CursorFirst && spec.HardwareCursor != nil {
				ui.SetShowHardwareCursor(*spec.HardwareCursor)
			}
			if spec.Overlay != nil {
				overlay := &oracleLines{}
				overlays = append(overlays, overlay)
				handles = append(handles, ui.ShowOverlay(overlay, spec.Overlay.Spec.options()))
			}
			if spec.HideOverlay && len(handles) > 0 {
				handles[len(handles)-1].Hide()
				handles, overlays = handles[:len(handles)-1], overlays[:len(overlays)-1]
			}
			for i, rendered := range sc.Log[step].Overlays {
				overlays[i].lines = rendered
			}
			// Each component returns what Pi's returned after this step; unchanged components keep their lines.
			for i, rendered := range sc.Log[step].Rendered {
				parts[i].lines = rendered
			}
			if spec.Resize != nil {
				ui.SetFixedSize(spec.Resize[0], spec.Resize[1])
			}
			if spec.ClearOnShrink != nil {
				ui.SetClearOnShrink(*spec.ClearOnShrink)
			}
			if spec.HardwareCursor != nil && !spec.CursorFirst {
				ui.SetShowHardwareCursor(*spec.HardwareCursor)
			}
			// A requested frame runs before the step's own render, as Pi's next-tick render does.
			if pending != nil {
				run := pending
				pending = nil
				run()
			}
			if spec.Force {
				ui.ForceFullRender()
			}
			// A step Pi threw on (a line wider than the terminal) must make Pig panic with the same message; the scenario ends there.
			message := renderPanicMessage(ui.Render)
			if want := sc.Log[step].Error; want != "" || message != "" {
				if want == "" || !strings.HasPrefix(message, want) {
					t.Errorf("scenario %d step %d: Pig panicked %q, Pi threw %q", index, step, message, want)
					failures++
				}
				break
			}
			if got, want := out.String(), sc.Log[step].Out; got != want {
				failures++
				if failures <= 6 {
					t.Errorf("scenario %d (%dx%d, nested %v) step %d %+v\n got %q\nwant %q", index, sc.Cols, sc.Rows, sc.Nested, step, spec, got, want)
				}
				break
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}

// oracleSize is upstream's SizeValue: a number or a percentage string.
type oracleSize struct {
	value   float64
	percent bool
	set     bool
}

func (s *oracleSize) UnmarshalJSON(data []byte) error {
	s.set = true
	var text string
	if json.Unmarshal(data, &text) == nil {
		s.percent = true
		_, err := fmt.Sscanf(strings.TrimSuffix(text, "%"), "%g", &s.value)
		return err
	}
	return json.Unmarshal(data, &s.value)
}

func (s *oracleSize) spec() *OverlayValue {
	if s == nil {
		return nil
	}
	return &OverlayValue{Value: s.value, Percent: s.percent}
}

type oracleOverlaySpec struct {
	Width, MaxHeight, Row, Col *oracleSize
	MinWidth                   *int
	Anchor                     string
	OffsetX, OffsetY           int
	Margin                     json.RawMessage
	NonCapturing               bool
}

func (s oracleOverlaySpec) options() OverlayOptions {
	spec := OverlaySpec{
		Width: s.Width.spec(), MinWidth: s.MinWidth, MaxHeight: s.MaxHeight.spec(), Anchor: s.Anchor,
		OffsetX: s.OffsetX, OffsetY: s.OffsetY, Row: s.Row.spec(), Col: s.Col.spec(), NonCapturing: s.NonCapturing,
	}
	if len(s.Margin) > 0 {
		var all int
		var sides struct{ Top, Right, Bottom, Left int }
		if json.Unmarshal(s.Margin, &all) == nil {
			spec.Margin = OverlayMarginAll(all)
		} else if json.Unmarshal(s.Margin, &sides) == nil {
			spec.Margin = OverlayMarginSpec{Top: sides.Top, Right: sides.Right, Bottom: sides.Bottom, Left: sides.Left}
		}
	}
	return spec.Options()
}

// newManualRenderTUI is NewWithOutput with its frame timer disabled, for tests that call Render themselves. Opening or hiding an overlay requests a
// frame, as in Pi; a real timer would render on its own goroutine while the test drives the same components.
func newManualRenderTUI(out io.Writer, cols, rows int) *TuiMainScreen {
	ui := NewWithOutput(out, cols, rows)
	ui.afterFunc = func(time.Duration, func()) stoppableTimer { return stoppedOracleTimer{} }
	return ui
}

// TestMainScreenFixtureIsCurrent reruns testdata/main-screen-oracle.mjs against the installed Pi and requires the committed fixture to be
// byte-identical, so the frames Pig is compared with are the pinned version's, not a stale capture.
func TestMainScreenFixtureIsCurrent(t *testing.T) {
	t.Parallel() // the generator waits on Pi's render throttle, about 30 s of wall time
	root, err := filepath.EvalSymlinks("../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Version != pigversion.UpstreamVersion {
		t.Fatalf("installed Pi %q (%v), want %s", manifest.Version, err, pigversion.UpstreamVersion)
	}
	dist, err := filepath.Abs(filepath.Join(root, "node_modules/@earendil-works/pi-tui/dist/index.js"))
	if err != nil {
		t.Fatal(err)
	}
	generated, err := exec.CommandContext(t.Context(), "node", "testdata/main-screen-oracle.mjs", dist).Output()
	if err != nil {
		t.Fatalf("generate fixture: %v", err)
	}
	file, err := os.Open("testdata/main-screen-oracle.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, committed) {
		t.Fatalf("testdata/main-screen-oracle.json.gz is not the output of the installed Pi %s: regenerate it with node tui/testdata/main-screen-oracle.mjs <pi-tui dist/index.js> | gzip", pigversion.UpstreamVersion)
	}
}

// renderPanicMessage runs render and returns the message it panicked with, or "" when it returned.
func renderPanicMessage(render func()) (message string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if err, ok := recovered.(error); ok {
				message = err.Error()
			} else {
				message = fmt.Sprint(recovered)
			}
		}
	}()
	render()
	return ""
}

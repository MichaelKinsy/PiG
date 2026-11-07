package tui

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// oracleSlot is one top-level part: either the lines Pi returned or a real Image component, as the step's set chose.
type oracleSlot struct {
	lines *oracleLines
	image *Image
}

func (s *oracleSlot) Render(width int) []string {
	if s.image != nil {
		return s.image.Render(width)
	}
	return s.lines.Render(width)
}

func (s *oracleSlot) Invalidate() {
	if s.image != nil {
		s.image.Invalidate()
	}
}

type oracleImageSpec struct {
	Data                          string
	Dims                          [2]int
	ImageID                       int
	MaxWidthCells, MaxHeightCells int
}

// setOracleEnv sets or clears an environment variable for the rest of the test.
func setOracleEnv(t *testing.T, name string, set bool, value string) {
	t.Helper()
	if set {
		t.Setenv(name, value)
		return
	}
	t.Setenv(name, "")
	_ = os.Unsetenv(name)
}

// altScreenOracleStartMarker is what the oracle terminal writes from Terminal.start; Pig's Start never calls a Terminal (the driver owns it), so
// the marker and the cursor hide Pi's start() issues right after it are the only bytes left out of the comparison.
// altScreenOracleStopMarker is what Pi's stop() writes between the renderer's own two writes: the terminal's showCursor and stop.
const altScreenOracleStopMarker = "\x1b[?25h<terminal.stop>"

const altScreenOracleStartMarker = "<terminal.start>\x1b[?25l"

// The fixture is the pinned Pi's TuiAltScreen run over seeded component trees (flat and nested containers, kitty and iTerm2 image lines),
// terminal sizes (including 1-row and 3-row terminals), and frame sequences with growth, shrink, in-place edits, resizes, forced renders, scrolling
// (by lines, to the top and to the bottom), overlays and hardware-cursor toggles, cursor markers, and unclipped components
// (testdata/alt-screen-oracle.mjs). It compares the bytes start() wrote and the bytes each step wrote; the components return the lines Pi's
// components returned at that step's size. Scrolling the rows that moved (D205) writes other bytes than Pi, so it is off here.
func TestAltScreenFramesMatchPiOracle(t *testing.T) {
	t.Setenv("PIG_TUI_SCROLL_ROWS", "0")
	checkAltScreenOracle(t, false)
}

// With the rows that moved scrolled (D205), a step writes other bytes than Pi's step wherever rows moved, and must show what
// Pi's step shows: Pi's bytes and PiG's are each replayed on a terminal (rowsVT), whose rows must be equal after every step.
func TestAltScreenScrolledFramesShowPisScreens(t *testing.T) {
	checkAltScreenOracle(t, true)
}

// A terminal that D205 does not trust with scroll regions gets Pi's bytes: with scrolling left on (PIG_TUI_SCROLL_ROWS unset), every
// step of every scenario of the pinned Pi's fixture writes exactly the bytes Pi's step wrote, as escaped_output_equal compares them.
// PIG_TUI_SCROLL_ROWS=0 in a terminal that would scroll does the same.
func TestAltScreenScrollFallsBackToPisBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"TERM=dumb", map[string]string{"TERM": "dumb"}},
		{"TERM unset", map[string]string{"TERM": ""}},
		{"TERM=vt52", map[string]string{"TERM": "vt52"}},
		{"TERM=unknown", map[string]string{"TERM": "unknown"}},
		{"PIG_TUI_SCROLL_ROWS=0 in xterm", map[string]string{"PIG_TUI_SCROLL_ROWS": "0"}},
		{"PIG_TUI_SCROLL_ROWS=0 with COLORTERM=truecolor", map[string]string{"PIG_TUI_SCROLL_ROWS": "0", "COLORTERM": "truecolor"}},
	} {
		t.Run(tc.name, func(t *testing.T) { checkAltScreenOracleIn(t, false, tc.env, nil) })
	}
}

// scrollRegionsUsableIn trusts the common terminals and multiplexers and no other.
func TestScrollRegionsUsableIn(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"TERM": "xterm-256color"}, true},
		{map[string]string{"TERM": "tmux-256color", "TMUX": "/tmp/tmux-1/default,1,0"}, true},
		{map[string]string{"TERM": "screen-256color", "STY": "1.pts-0"}, true},
		{map[string]string{"TERM": "xterm-256color", "WT_SESSION": "1"}, true},
		{map[string]string{"TERM": "xterm-kitty"}, true},
		{map[string]string{"TERM": "linux"}, true},
		{map[string]string{"TERM": "xterm-256color", "PIG_TUI_SCROLL_ROWS": "1"}, true},
		{map[string]string{"TERM": "xterm-256color", "PIG_TUI_SCROLL_ROWS": "0"}, false},
		{map[string]string{"TERM": "xterm-256color", "ZELLIJ": "0"}, false},
		{map[string]string{"TERM": "dumb"}, false},
		{map[string]string{"TERM": "DUMB"}, false},
		{map[string]string{"TERM": "unknown"}, false},
		{map[string]string{"TERM": "vt52"}, false},
		{map[string]string{}, false},
	} {
		if got := scrollRegionsUsableIn(func(k string) string { return tc.env[k] }); got != tc.want {
			t.Errorf("scrollRegionsUsableIn(%v) = %v, want %v", tc.env, got, tc.want)
		}
	}
}

// checkAltScreenOracle runs the fixture's scenarios and compares each step's bytes with Pi's, or with screens set, the rows
// they show.
func checkAltScreenOracle(t *testing.T, screens bool) {
	checkAltScreenOracleIn(t, screens, nil, nil)
}

// oracleTap observes the bytes a replay of the oracle's scenarios writes: start is called with the bytes Start wrote and step with the
// bytes of each step, the size the step resized the screen to (nil when it did not), and whether a line the step's components rendered is wider
// than the screen (the renderer clips such a line, which drops its closing SGR reset). The terminal-engine corpus records them, and leaves
// judging what the steps show to the engines (recordOnly), so a step that shows other rows than Pi's reaches them instead of failing here.
type oracleTap struct {
	recordOnly bool
	// closeClipped closes every rendered line wider than the screen before the replay uses it: the line is clipped to the screen and ends with a
	// reset, as the line a width-aware component renders. The bytes are then not Pi's, so they are not compared with Pi's.
	closeClipped bool
	start        func(scenario, cols, rows int, images string, out string)
	step         func(scenario int, resize []int, overWide bool, out string)
}

// checkAltScreenOracleIn is checkAltScreenOracle in a terminal whose environment sets env over the oracle's (TERM=xterm-256color
// and no multiplexer variables). A non-nil tap sees every scenario's bytes.
func checkAltScreenOracleIn(t *testing.T, screens bool, env map[string]string, tap *oracleTap) {
	file, err := os.Open("testdata/alt-screen-oracle.json.gz")
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
		Wez, Tmux         bool
		LayoutRoot        bool
		Preserve          bool
		StopOut           string
		ImagesAfterStart  string
		ImagesAfterStop   string
		StartOut          string
		Steps             []struct {
			Set            map[string]json.RawMessage
			Resize         []int
			Force          bool
			HardwareCursor *bool
			CursorFirst    bool
			ScrollBy       *int
			ScrollTop      bool
			ScrollBottom   bool
			Overlay        *struct {
				Spec  oracleOverlaySpec
				Lines []string
			}
			HideOverlay bool
			Flash       *struct {
				Message    string
				DurationMs int
			}
		}
		Log []struct {
			FullRedraws int
			Out         string
			Rendered    [][]string
			Overlays    [][]string
			Error       string
		}
	}
	if err := json.NewDecoder(zr).Decode(&scenarios); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TMUX", "ZELLIJ", "STY", "WEZTERM_PANE", "TERM_PROGRAM"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	t.Setenv("TERM", "xterm-256color")
	for name, value := range env {
		t.Setenv(name, value)
	}
	t.Cleanup(ResetCapabilitiesCache)
	// The generator runs every scenario in one fresh Node process: Pi's module-level Kitty image registry starts empty and carries over from
	// scenario to scenario, deciding whether a frame re-transmits an image line or re-places it. Start from that empty registry, not from
	// the images earlier tests or an earlier -count run registered, and leave none behind.
	resetKittyRegistry(t)
	t.Cleanup(func() { resetKittyRegistry(t) })
	recordOnly, closeClipped := tap != nil && tap.recordOnly, tap != nil && tap.closeClipped
	failures, scrolledSteps := 0, 0
	report := func(format string, args ...any) {
		if failures++; failures <= 6 {
			t.Errorf(format, args...)
		}
	}
	for index, sc := range scenarios {
		setOracleEnv(t, "TERM_PROGRAM", sc.Wez, "WezTerm")
		setOracleEnv(t, "TMUX", sc.Tmux, "1")
		SetCellDimensions(CellDimensions{WidthPx: 9, HeightPx: 18})
		SetCapabilities(TerminalCapabilities{Images: ImageProtocol(sc.Images), TrueColor: true, Hyperlinks: true})
		var out strings.Builder
		ui := NewTuiAltScreenWithOutput(&out, sc.Cols, sc.Rows, TuiAltScreenOptions{})
		var pending func()
		ui.afterFunc = func(_ time.Duration, fn func()) stoppableTimer {
			pending = fn
			return stoppedOracleTimer{}
		}
		parts := make([]*oracleSlot, sc.Parts)
		for i := range parts {
			parts[i] = &oracleSlot{lines: &oracleLines{}}
		}
		host := ui.Add
		if sc.LayoutRoot {
			root := NewContainer()
			host = root.Add
			ui.SetLayoutRoot(root)
		}
		if sc.Nested && sc.Parts > 1 {
			inner := NewContainer()
			for _, part := range parts[1:] {
				inner.Add(part)
			}
			host(parts[0])
			host(inner)
		} else {
			for _, part := range parts {
				host(part)
			}
		}
		ui.Start()
		if got, want := out.String(), strings.ReplaceAll(sc.StartOut, altScreenOracleStartMarker, ""); got != want {
			report("scenario %d (%dx%d, images %q) start\n got %q\nwant %q", index, sc.Cols, sc.Rows, sc.Images, got, want)
			continue
		}
		if got := string(GetCapabilities().Images); got != sc.ImagesAfterStart {
			report("scenario %d: images %q after start, Pi %q", index, got, sc.ImagesAfterStart)
		}
		if tap != nil {
			tap.start(index, sc.Cols, sc.Rows, sc.Images, out.String())
		}
		cols := sc.Cols
		pigVT, piVT := newRowsVT(max(1, sc.Rows)), newRowsVT(max(1, sc.Rows))
		pigVT.apply(out.String())
		piVT.apply(strings.ReplaceAll(sc.StartOut, altScreenOracleStartMarker, ""))
		pending = nil
		var overlays []*oracleLines
		var handles []*OverlayHandle
		for step, spec := range sc.Steps {
			out.Reset()
			if spec.Overlay != nil {
				overlay := &oracleLines{}
				overlays = append(overlays, overlay)
				handles = append(handles, ui.ShowOverlay(overlay, spec.Overlay.Spec.options()))
			}
			if spec.HideOverlay && len(handles) > 0 {
				handles[len(handles)-1].Hide()
				handles, overlays = handles[:len(handles)-1], overlays[:len(overlays)-1]
			}
			if spec.Resize != nil {
				cols = spec.Resize[0]
			}
			closed := func(lines []string) []string {
				if !closeClipped {
					return lines
				}
				result := make([]string, len(lines))
				for i, line := range lines {
					if widthx.VisibleWidth(line) > cols {
						line = widthx.TruncateToWidth(line, cols, "", false) + "\x1b[0m"
					}
					result[i] = line
				}
				return result
			}
			for i, rendered := range sc.Log[step].Overlays {
				overlays[i].lines = closed(rendered)
			}
			for k, raw := range spec.Set {
				index := 0
				if _, err := fmt.Sscan(k, &index); err != nil {
					t.Fatal(err)
				}
				var image *oracleImageSpec
				var wrapper struct{ Image *oracleImageSpec }
				if json.Unmarshal(raw, &wrapper) == nil && wrapper.Image != nil {
					image = wrapper.Image
				}
				parts[index].image = nil
				if image != nil {
					options := ImageOptions{ImageID: image.ImageID, MaxWidthCells: image.MaxWidthCells, MaxHeightCells: image.MaxHeightCells}
					parts[index].image = NewImage(image.Data, "image/png", ImageTheme{}, options, &ImageDimensions{WidthPx: image.Dims[0], HeightPx: image.Dims[1]})
				}
			}
			for i, rendered := range sc.Log[step].Rendered {
				if parts[i].image == nil {
					parts[i].lines.lines = closed(rendered)
				}
			}
			if spec.Resize != nil {
				ui.SetFixedSize(spec.Resize[0], spec.Resize[1])
			}
			setCursor := func() {
				if spec.HardwareCursor != nil {
					ui.SetShowHardwareCursor(*spec.HardwareCursor)
				}
			}
			if spec.Flash != nil {
				ui.Flash(spec.Flash.Message, spec.Flash.DurationMs)
			}
			if spec.CursorFirst {
				setCursor()
			}
			if spec.ScrollBy != nil {
				ui.ScrollBy(*spec.ScrollBy)
			}
			if spec.ScrollTop {
				ui.ScrollToTop()
			}
			if spec.ScrollBottom {
				ui.ScrollToBottom()
			}
			if !spec.CursorFirst {
				setCursor()
			}
			// A requested frame runs before the step's own render, as Pi's next-tick render does.
			if pending != nil {
				run := pending
				pending = nil
				run()
			}
			ui.RenderNow(spec.Force)
			if got, want := ui.FullRedraws(), sc.Log[step].FullRedraws; got != want {
				report("scenario %d (%dx%d) step %d: %d full redraws, Pi %d", index, sc.Cols, sc.Rows, step, got, want)
				break
			}
			if tap != nil {
				overWide := false
				for _, lines := range append(slices.Clone(sc.Log[step].Rendered), sc.Log[step].Overlays...) {
					for _, line := range lines {
						overWide = overWide || widthx.VisibleWidth(line) > cols
					}
				}
				tap.step(index, spec.Resize, overWide, out.String())
			}
			if screens && !recordOnly {
				// A new height redraws the whole screen; a resize to the same size draws nothing.
				if spec.Resize != nil && max(1, spec.Resize[1]) != len(pigVT.rows) {
					pigVT, piVT = newRowsVT(max(1, spec.Resize[1])), newRowsVT(max(1, spec.Resize[1]))
				}
				pigVT.apply(out.String())
				piVT.apply(sc.Log[step].Out)
				if !slices.Equal(pigVT.rows, piVT.rows) {
					var rows strings.Builder
					for row := range max(len(pigVT.rows), len(piVT.rows)) {
						if pig, pi := altScreenRow(pigVT.rows, row), altScreenRow(piVT.rows, row); pig != pi {
							fmt.Fprintf(&rows, "\nrow %d: PiG %q\n        Pi  %q", row, pig, pi)
						}
					}
					report("scenario %d (%dx%d, images %q) step %d %+v shows other rows than Pi's step:%s\nPiG wrote %q\nPi wrote  %q", index, sc.Cols, sc.Rows, sc.Images, step, spec, rows.String(), out.String(), sc.Log[step].Out)
					break
				}
				if out.String() != sc.Log[step].Out {
					scrolledSteps++
				}
			} else if got, want := out.String(), sc.Log[step].Out; !screens && !closeClipped && got != want {
				report("scenario %d (%dx%d, images %q) step %d %+v\n got %q\nwant %q", index, sc.Cols, sc.Rows, sc.Images, step, spec, got, want)
				break
			}
		}
		out.Reset()
		ui.StopWithOptions(StopOptions{PreserveScreen: sc.Preserve})
		if got, want := out.String(), strings.ReplaceAll(sc.StopOut, altScreenOracleStopMarker, ""); !closeClipped && got != want {
			report("scenario %d (%dx%d, images %q, preserve %v) stop\n got %q\nwant %q", index, sc.Cols, sc.Rows, sc.Images, sc.Preserve, got, want)
		}
		if got := string(GetCapabilities().Images); got != sc.ImagesAfterStop {
			report("scenario %d: images %q after stop, Pi %q", index, got, sc.ImagesAfterStop)
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
	if screens && !recordOnly {
		t.Logf("%d steps scrolled rows and wrote other bytes than Pi", scrolledSteps)
		if scrolledSteps == 0 {
			t.Error("no step scrolled rows, so no step was compared by what it shows")
		}
	}
}

// TestAltScreenFixtureIsCurrent reruns testdata/alt-screen-oracle.mjs against the installed Pi and requires the committed fixture to be
// byte-identical, so the frames Pig is compared with are the pinned version's, not a stale capture.
func TestAltScreenFixtureIsCurrent(t *testing.T) {
	t.Parallel()
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
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Version != pigversion.UpstreamPackageVersion {
		t.Fatalf("installed Pi %q (%v), want %s", manifest.Version, err, pigversion.UpstreamPackageVersion)
	}
	dist, err := filepath.Abs(filepath.Join(root, "node_modules/@earendil-works/pi-tui/dist/index.js"))
	if err != nil {
		t.Fatal(err)
	}
	generated, err := piOracleCommand(t, "testdata/alt-screen-oracle.mjs", dist).Output()
	if err != nil {
		t.Fatalf("generate fixture: %v", err)
	}
	file, err := os.Open("testdata/alt-screen-oracle.json.gz")
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
		t.Fatalf("testdata/alt-screen-oracle.json.gz is not the output of the installed Pi %s: regenerate it with node tui/testdata/alt-screen-oracle.mjs <pi-tui dist/index.js> | gzip", pigversion.UpstreamVersion)
	}
}

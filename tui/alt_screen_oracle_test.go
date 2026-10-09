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
// components returned at that step's size.
func TestAltScreenFramesMatchPiOracle(t *testing.T) {
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
	t.Cleanup(ResetCapabilitiesCache)
	failures := 0
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
			for i, rendered := range sc.Log[step].Overlays {
				overlays[i].lines = rendered
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
					parts[i].lines.lines = rendered
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
			if got, want := out.String(), sc.Log[step].Out; got != want {
				report("scenario %d (%dx%d, images %q) step %d %+v\n got %q\nwant %q", index, sc.Cols, sc.Rows, sc.Images, step, spec, got, want)
				break
			}
		}
		out.Reset()
		ui.StopWithOptions(StopOptions{PreserveScreen: sc.Preserve})
		if got, want := out.String(), strings.ReplaceAll(sc.StopOut, altScreenOracleStopMarker, ""); got != want {
			report("scenario %d (%dx%d, images %q, preserve %v) stop\n got %q\nwant %q", index, sc.Cols, sc.Rows, sc.Images, sc.Preserve, got, want)
		}
		if got := string(GetCapabilities().Images); got != sc.ImagesAfterStop {
			report("scenario %d: images %q after stop, Pi %q", index, got, sc.ImagesAfterStop)
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}

// TestAltScreenFixtureIsCurrent reruns testdata/alt-screen-oracle.mjs against the installed Pi and requires the committed fixture to be
// byte-identical, so the frames Pig is compared with are the pinned version's, not a stale capture.
func TestAltScreenFixtureIsCurrent(t *testing.T) {
	t.Parallel() // the generator waits on Pi's render throttle, about a minute of wall time
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
	generated, err := exec.CommandContext(t.Context(), "node", "testdata/alt-screen-oracle.mjs", dist).Output()
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

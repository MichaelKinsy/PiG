// Byte-for-byte comparison of lane E's components with p2's own TypeScript components (apps/p2/src/tui/components of the pinned Pi
// mirror, `.upstream/current`), run by Node over the installed pi-tui (testdata/oracle_probe.mjs). Every scene is built from the
// same data on both sides, rendered at several widths in several themes, and driven by the same inputs; each frame must match.
//
// Set up the oracle with `python3 automation/pin/pin.py install-oracle && make upstream-mirror`.
package components

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// oracleKeyText is the key text of the oracle context: Pi's probe has the same table.
var oracleKeyText = map[string]string{"tui.select.confirm": "enter", "tui.select.cancel": "escape", "app.interrupt": "esc", "app.expandTools": "ctrl+o"}

// oracleContext is the testContext with the key texts of the probe, in a theme and color mode.
type oracleContext struct{ *testContext }

func (oracleContext) KeyText(id string) string {
	if text, ok := oracleKeyText[id]; ok {
		return text
	}
	return "ctrl+o"
}

func newOracleContext(t testing.TB, name string, mode pitui.TerminalColorMode) oracleContext {
	t.Helper()
	json, ok := theme.BuiltinTheme(name)
	if !ok {
		t.Fatalf("no builtin theme %q", name)
	}
	built, err := theme.CreateTheme(json, mode, theme.TerminalState{})
	if err != nil {
		t.Fatal(err)
	}
	return oracleContext{&testContext{themeRef: theme.NewThemeRef(built)}}
}

// oracleScene is one component with the inputs sent to it: its rows are compared after the initial state and after each input.
type oracleScene struct {
	component pitui.Component
	// focus is the focused flag (new(true)) set before the first render, nil to leave it.
	focus  *bool
	inputs []string
	// actions are the setters "@name" inputs run.
	actions map[string]func()
	// cleanup releases the scene's timers.
	cleanup func()
}

var oracleScenes = map[string]func(t testing.TB, ctx oracleContext) oracleScene{}

func registerScene(name string, build func(t testing.TB, ctx oracleContext) oracleScene) {
	if _, ok := oracleScenes[name]; ok {
		panic("duplicate scene " + name)
	}
	oracleScenes[name] = build
}

type oracleProbe struct {
	Scene string `json:"scene"`
	Width int    `json:"width"`
	Theme string `json:"theme"`
	Mode  string `json:"mode"`
}

// oracleSetups are the themes and color modes every scene runs in.
var oracleSetups = []struct {
	theme string
	mode  pitui.TerminalColorMode
}{{"dark", pitui.TerminalColorModeTrueColor}, {"light", pitui.TerminalColorModeTrueColor}, {"dark", pitui.TerminalColorMode256}}

var oracleWidths = []int{1, 4, 10, 24, 40, 80, 120}

func repoRoot(t testing.TB) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}

// pinnedPackageVersion is the Pi package version the probe must be running against (.upstream/pin package_version).
func pinnedPackageVersion(t testing.TB, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".upstream", "pin"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "package_version="); ok {
			return strings.TrimSpace(value)
		}
	}
	t.Fatal("package_version missing in .upstream/pin")
	return ""
}

// piFrames runs the probes through Node, one frame list per probe.
func piFrames(t testing.TB, probes []oracleProbe) [][][]string {
	t.Helper()
	root := repoRoot(t)
	p2, err := filepath.EvalSymlinks(filepath.Join(root, ".upstream", "current", "apps", "p2"))
	if err != nil {
		t.Fatalf("Pi mirror missing (run `python3 automation/pin/pin.py install-oracle && make upstream-mirror`): %v", err)
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--import", "file://"+filepath.Join(p2, "src", "source.ts"),
		filepath.Join("testdata", "oracle_probe.mjs"), p2, pinnedPackageVersion(t, root))
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var frames [][][]string
	if err := json.Unmarshal(output, &frames); err != nil {
		t.Fatal(err)
	}
	if len(frames) != len(probes) {
		t.Fatalf("Pi returned %d results for %d probes", len(frames), len(probes))
	}
	return frames
}

// goFrames is the rows of the scene's initial state and after each input.
func goFrames(t testing.TB, probe oracleProbe) [][]string {
	t.Helper()
	ctx := newOracleContext(t, probe.Theme, pitui.TerminalColorMode(probe.Mode))
	scene := oracleScenes[probe.Scene](t, ctx)
	if scene.cleanup != nil {
		defer scene.cleanup()
	}
	if scene.focus != nil {
		scene.component.(pitui.Focusable).SetFocused(*scene.focus)
	}
	frames := [][]string{nonNilRows(scene.component.Render(probe.Width))}
	for _, input := range scene.inputs {
		// "@name" runs a scene action (a setter), anything else is typed.
		if name, ok := strings.CutPrefix(input, "@"); ok {
			scene.actions[name]()
		} else {
			scene.component.(pitui.InputHandler).HandleInput(input)
		}
		frames = append(frames, nonNilRows(scene.component.Render(probe.Width)))
	}
	return frames
}

func nonNilRows(rows []string) []string {
	if rows == nil {
		return []string{}
	}
	return rows
}

// TestComponentsRenderLikeP2 compares every scene with p2's TypeScript component.
func TestComponentsRenderLikeP2(t *testing.T) {
	var probes []oracleProbe
	names := make([]string, 0, len(oracleScenes))
	for name := range oracleScenes {
		names = append(names, name)
	}
	slices.Sort(names)
	// PIG_ORACLE_SCENE (a substring) and PIG_ORACLE_WIDTH narrow a run to the scenes and the width being debugged.
	only, onlyWidth := os.Getenv("PIG_ORACLE_SCENE"), os.Getenv("PIG_ORACLE_WIDTH")
	for _, name := range names {
		if !strings.Contains(name, only) {
			continue
		}
		for _, setup := range oracleSetups {
			for _, width := range oracleWidths {
				// D66: at width 1 the shared Editor highlights the last grapheme where Pi's fake cursor is two cells wide.
				if width == 1 && strings.HasPrefix(name, "editor-") {
					continue
				}
				if onlyWidth != "" && onlyWidth != strconv.Itoa(width) {
					continue
				}
				probes = append(probes, oracleProbe{Scene: name, Width: width, Theme: setup.theme, Mode: string(setup.mode)})
			}
		}
	}
	expected := piFrames(t, probes)
	divergent := divergentScenes(t)
	failures := 0
	reported := map[string]bool{}
	failing := map[string][]string{}
	ran := map[string]bool{}
	for i, probe := range probes {
		got := goFrames(t, probe)
		ran[probe.Scene] = true
		if reflect.DeepEqual(got, expected[i]) {
			continue
		}
		if _, known := divergent[probe.Scene]; known {
			divergent[probe.Scene] = true
			continue
		}
		failures++
		failing[probe.Scene] = append(failing[probe.Scene], fmt.Sprintf("%d/%s/%s", probe.Width, probe.Theme, probe.Mode))
		if !reported[probe.Scene] {
			reported[probe.Scene] = true
			t.Errorf("%s", describeDiff(probe, expected[i], got))
		}
	}
	for scene, setups := range failing {
		t.Logf("%s differs at width/theme/mode: %s", scene, strings.Join(setups, " "))
	}
	for scene, differs := range divergent {
		if ran[scene] && !differs {
			t.Errorf("%s now matches p2: remove it from testdata/oracle_divergent.txt", scene)
		}
	}
	if failures > 0 {
		t.Fatalf("%d of %d probes differ from p2's TypeScript", failures, len(probes))
	}
}

// divergentScenes are the scenes of testdata/oracle_divergent.txt (D480): known to differ from p2, so the value is whether a probe differed.
func divergentScenes(t testing.TB) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "oracle_divergent.txt"))
	if err != nil {
		t.Fatal(err)
	}
	scenes := map[string]bool{}
	for line := range strings.SplitSeq(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			scenes[line] = false
		}
	}
	return scenes
}

func describeDiff(probe oracleProbe, pi, got [][]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s width %d %s/%s:", probe.Scene, probe.Width, probe.Theme, probe.Mode)
	if len(pi) != len(got) {
		fmt.Fprintf(&b, " %d frames in Pi, %d in Go", len(pi), len(got))
		return b.String()
	}
	for frame := range pi {
		if reflect.DeepEqual(pi[frame], got[frame]) {
			continue
		}
		fmt.Fprintf(&b, " frame %d differs (%d rows in Pi, %d in Go)", frame, len(pi[frame]), len(got[frame]))
		for row := range max(len(pi[frame]), len(got[frame])) {
			var p, g string
			if row < len(pi[frame]) {
				p = pi[frame][row]
			}
			if row < len(got[frame]) {
				g = got[frame][row]
			}
			if p != g {
				fmt.Fprintf(&b, "\n row %d\n  pi: %q\n  go: %q", row, p, g)
				break
			}
		}
		break
	}
	return b.String()
}

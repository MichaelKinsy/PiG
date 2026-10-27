//go:build termengines

package termengines

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The corpus (tui.TestAltScreenTermEngineCorpus) holds each scenario twice: variant A is what PiG's alternate-screen renderer writes with scroll
// regions (D205), variant B what it writes with PIG_TUI_SCROLL_ROWS=0, which are Pi's bytes. Each engine's runner replays both through its own
// terminal parser, one step (frame) at a time, and prints the screen after every step. The screens of A and B must be equal after every step.
// There is no golden screen: A is judged against B on the same engine, so an engine's own quirks cancel and a difference is a frame that
// shows other rows, styles or cursor position in that terminal when it scrolls rows.

type planStep struct {
	end    int
	resize []int
}

type planVariant struct {
	file  string
	steps []planStep
	data  []byte
}

type planScenario struct {
	name       string
	cols, rows int
	variants   map[string]*planVariant
}

func readPlan(t *testing.T, dir string) []*planScenario {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "plan.txt"))
	if err != nil {
		t.Fatalf("read the corpus plan (make test-term-engines generates the corpus): %v", err)
	}
	var scenarios []*planScenario
	var variant *planVariant
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		fields := strings.Split(line, "\t")
		num := func(i int) int {
			n, err := strconv.Atoi(fields[i])
			if err != nil {
				t.Fatalf("plan line %q: %v", line, err)
			}
			return n
		}
		switch fields[0] {
		case "scenario":
			scenarios = append(scenarios, &planScenario{name: fields[1], cols: num(2), rows: num(3), variants: map[string]*planVariant{}})
		case "variant":
			variant = &planVariant{file: fields[2]}
			if variant.data, err = os.ReadFile(filepath.Join(dir, fields[2])); err != nil {
				t.Fatal(err)
			}
			scenarios[len(scenarios)-1].variants[fields[1]] = variant
		case "step":
			step := planStep{end: num(1)}
			if len(fields) > 2 {
				step.resize = []int{num(2), num(3)}
			}
			variant.steps = append(variant.steps, step)
		default:
			t.Fatalf("plan line %q", line)
		}
	}
	return scenarios
}

// checkpoint is the screen after a step: the rows' text, the style runs ([column, width, style] per row) and the cursor.
type checkpoint struct {
	Cursor [2]int
	Rows   []string
	Styles json.RawMessage
}

type runnerLine struct {
	Engine, Version string
	Scenario        string
	Variant         string
	Checkpoints     []checkpoint
}

type engine struct {
	name    string
	env     string // names the runner command, set by automation/ci/test-term-engines.sh
	version func(t *testing.T) string
}

// lockVersion is the version Cargo.lock pins the package to, with the commit of a git source.
func lockVersion(t *testing.T, lock, name string) string {
	t.Helper()
	raw, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^name = "` + regexp.QuoteMeta(name) + `"\nversion = "([^"]+)"(?:\nsource = "[^"#]*#([0-9a-f]{8}))?`).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("%s pins no %s", lock, name)
	}
	if len(m[2]) > 0 {
		return string(m[1]) + "@" + string(m[2])
	}
	return string(m[1])
}

func libvtermVersion(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../automation/dev/toolchains.lock")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if fields := strings.Split(line, "\t"); len(fields) > 1 && fields[0] == "libvterm" {
			return fields[1]
		}
	}
	t.Fatal("automation/dev/toolchains.lock has no libvterm row")
	return ""
}

var engines = []engine{
	{"xterm.js", "PIG_TERM_ENGINE_XTERM", nil},
	{"alacritty_terminal", "PIG_TERM_ENGINE_ALACRITTY", func(t *testing.T) string {
		return lockVersion(t, "alacritty/Cargo.lock", "alacritty_terminal")
	}},
	{"wezterm-term", "PIG_TERM_ENGINE_WEZTERM", func(t *testing.T) string { return lockVersion(t, "wezterm/Cargo.lock", "wezterm-term") }},
	{"libvterm", "PIG_TERM_ENGINE_LIBVTERM", libvtermVersion},
	{"pyte", "PIG_TERM_ENGINE_PYTE", nil},
}

// run starts the engine's runner on the corpus and returns what it printed for every scenario and variant.
func (e engine) run(t *testing.T, dir string) (version string, screens map[string]map[string][]checkpoint) {
	t.Helper()
	command := strings.Fields(os.Getenv(e.env))
	if len(command) == 0 {
		t.Fatalf("%s is not set: run make test-term-engines, which builds the pinned engines and sets it", e.env)
	}
	cmd := exec.Command(command[0], append(command[1:], dir)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %v: %v", command, err)
	}
	screens = map[string]map[string][]checkpoint{}
	reader := bufio.NewReaderSize(out, 1<<20)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var l runnerLine
			if err := json.Unmarshal(line, &l); err != nil {
				t.Fatalf("%s printed a line that is not JSON (%v): %.200s", e.name, err, line)
			}
			if l.Engine != "" {
				version = l.Version
				continue
			}
			if screens[l.Scenario] == nil {
				screens[l.Scenario] = map[string][]checkpoint{}
			}
			screens[l.Scenario][l.Variant] = l.Checkpoints
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("%v failed: %v\n%s", command, err, stderr.String())
	}
	if e.version != nil {
		version = e.version(t)
	}
	return version, screens
}

func quoteTail(b []byte) string {
	const limit = 600
	if len(b) > limit {
		return fmt.Sprintf("...%q", b[len(b)-limit:])
	}
	return fmt.Sprintf("%q", b)
}

// cursorVisible is whether the terminal shows its cursor after stream: the last DECTCEM (CSI ? 25 h or l) in it, shown at the start.
// A hidden cursor's position cannot be seen, and the next frame addresses its rows absolutely, so only a shown cursor's position is compared.
func cursorVisible(stream []byte) bool {
	return bytes.LastIndex(stream, []byte("\x1b[?25h")) >= bytes.LastIndex(stream, []byte("\x1b[?25l"))
}

// describe is how checkpoints a and b differ, where the cursor is shown after the step in A and B as visibleA and visibleB say.
func describe(a, b checkpoint, visibleA, visibleB bool) string {
	var d strings.Builder
	if visibleA != visibleB {
		fmt.Fprintf(&d, "\n  cursor shown: A %v, B %v", visibleA, visibleB)
	} else if visibleA && a.Cursor != b.Cursor {
		fmt.Fprintf(&d, "\n  cursor (x,y): A %v, B %v", a.Cursor, b.Cursor)
	}
	var styleA, styleB [][]json.RawMessage
	_ = json.Unmarshal(a.Styles, &styleA)
	_ = json.Unmarshal(b.Styles, &styleB)
	for row := range max(len(a.Rows), len(b.Rows)) {
		var ra, rb string
		if row < len(a.Rows) {
			ra = a.Rows[row]
		}
		if row < len(b.Rows) {
			rb = b.Rows[row]
		}
		if ra != rb {
			fmt.Fprintf(&d, "\n  row %d text: A %q\n               B %q", row, ra, rb)
			continue
		}
		var sa, sb []json.RawMessage
		if row < len(styleA) {
			sa = styleA[row]
		}
		if row < len(styleB) {
			sb = styleB[row]
		}
		if fmt.Sprint(sa) != fmt.Sprint(sb) {
			fmt.Fprintf(&d, "\n  row %d (%q) styles: A %s\n                  B %s", row, ra, joinRaw(sa), joinRaw(sb))
		}
	}
	return d.String()
}

func joinRaw(runs []json.RawMessage) string {
	parts := make([]string, len(runs))
	for i, r := range runs {
		parts[i] = string(r)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func blank(c checkpoint) bool {
	for _, row := range c.Rows {
		if strings.TrimSpace(row) != "" {
			return false
		}
	}
	return true
}

// firstMismatch is the first step after which the screens of A and B differ, with how they differ; step is -1 when they never do.
func firstMismatch(a, b []checkpoint, pa, pb *planVariant) (step int, difference string) {
	for i := range a {
		difference = describe(a[i], b[i], cursorVisible(pa.data[:pa.steps[i].end]), cursorVisible(pb.data[:pb.steps[i].end]))
		if difference != "" {
			return i, difference
		}
	}
	return -1, ""
}

// stepBytes is what the terminal receives at step i of the variant.
func stepBytes(v *planVariant, i int) []byte {
	start := 0
	if i > 0 {
		start = v.steps[i-1].end
	}
	return v.data[start:v.steps[i].end]
}

// TestScrollRegionsShowTheSameScreenAsPisBytes replays A and B of every scenario through each engine and requires the same screen after every
// step. A mismatch is reported with the first differing step's bytes, which is the minimal sequence that makes the engine disagree with itself.
// A scenario named known-* is a recorded finding: it must differ in every engine, and fails once it no longer does, so that a fix deletes it.
func TestScrollRegionsShowTheSameScreenAsPisBytes(t *testing.T) {
	dir := os.Getenv("PIG_TERM_ENGINE_CORPUS_DIR")
	if dir == "" {
		t.Fatal("PIG_TERM_ENGINE_CORPUS_DIR is not set: run make test-term-engines, which generates the corpus")
	}
	plan := readPlan(t, dir)
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			t.Parallel()
			version, screens := e.run(t, dir)
			compared, mismatched, known, nonBlank := 0, 0, 0, 0
			for _, sc := range plan {
				got := screens[sc.name]
				a, b := got["A"], got["B"]
				pa, pb := sc.variants["A"], sc.variants["B"]
				if len(a) != len(pa.steps) || len(b) != len(pb.steps) {
					t.Errorf("%s: %d/%d screens printed for %d/%d steps", sc.name, len(a), len(b), len(pa.steps), len(pb.steps))
					continue
				}
				for i := range a {
					if !blank(a[i]) {
						nonBlank++
					}
				}
				step, difference := firstMismatch(a, b, pa, pb)
				switch isKnown := strings.HasPrefix(sc.name, "known-"); {
				case step < 0 && isKnown:
					t.Errorf("%s: %s shows the same screens for A and B after all %d steps: the finding is fixed, delete the scenario", sc.name, e.name, len(a))
				case step < 0:
					compared += len(a)
				case isKnown:
					known++
					t.Logf("finding %s (%dx%d) step %d: %s shows other screens for A and B:%s\nstep bytes A: %s\nstep bytes B: %s",
						sc.name, sc.cols, sc.rows, step, e.name, difference, quoteTail(stepBytes(pa, step)), quoteTail(stepBytes(pb, step)))
				default:
					compared += step
					mismatched++
					t.Errorf("%s (%dx%d) step %d (resize %v): %s shows other screens for A (scroll regions) and B (Pi's bytes):%s\nstep bytes A: %s\nstep bytes B: %s",
						sc.name, sc.cols, sc.rows, step, pa.steps[step].resize, e.name, difference, quoteTail(stepBytes(pa, step)), quoteTail(stepBytes(pb, step)))
				}
			}
			t.Logf("%s %s: %d scenarios, %d checkpoints per variant identical, %d scenarios differ, %d known findings reproduce", e.name, version, len(plan), compared, mismatched, known)
			if compared == 0 || nonBlank < compared/4 {
				t.Errorf("%s showed text after only %d of %d steps: the replay did not draw the frames", e.name, nonBlank, compared)
			}
		})
	}
}

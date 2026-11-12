//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// pig additive (D614): the terminal view client in a real interactive session. A fused Go SDK fixture
// (testdata/ui-view/fixture) mounts a panel and a widget, and the test drives them with keys through tmux.

const uiViewModels = `{"providers":{"test-faux":{"baseUrl":"http://localhost:0","api":"test-faux","authHeader":false,"models":[{"id":"faux-1","name":"Test Faux","api":"test-faux","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}}}`

type uiViewRun struct {
	*harness
	home, cwd string
	pipe      string
}

// clean replaces the run's own directories, which differ between any two runs.
func (r *uiViewRun) clean(s string) string {
	return strings.NewReplacer(r.home, "<home>", r.cwd, "<cwd>").Replace(s)
}

// startUIView starts pig with the fixture under piglet (on.yaml or off.yaml) in TUI mode, the fixture's mounts on or
// off.
func startUIView(t *testing.T, piglet, mode string, mounts bool) *uiViewRun {
	t.Helper()
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(uiViewModels), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"tuiMode":"`+mode+`","quietStartup":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	flag := "1"
	if !mounts {
		flag = "0"
	}
	h := newHarness(t)
	cwd := t.TempDir()
	h.startArgsAt([]string{"--piglet", filepath.Join(root, "test", "integration", "testdata", "ui-view", piglet), "--model", "test-faux/faux-1"}, cwd,
		"PIG_HOME="+home, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_UI_VIEW_FIXTURE_MOUNTS="+flag)
	// The first start builds the fixture.
	h.expectContains(4*time.Minute, interactiveReadyMarker)
	return &uiViewRun{harness: h, home: home, cwd: cwd}
}

// settle waits until the pane shows every needle, or, with none, until it stopped changing.
func (r *uiViewRun) settle(needles ...string) string {
	r.t.Helper()
	if len(needles) > 0 {
		r.expectContains(20*time.Second, needles...)
	}
	last := ""
	for range 40 {
		time.Sleep(250 * time.Millisecond)
		pane := r.capture()
		if pane == last {
			return pane
		}
		last = pane
	}
	r.t.Fatalf("the pane did not settle:\n%s", last)
	return ""
}

func (r *uiViewRun) gone(needles ...string) {
	r.t.Helper()
	pane := r.settle()
	for _, needle := range needles {
		if strings.Contains(pane, needle) {
			r.t.Fatalf("%q is still on the screen:\n%s", needle, pane)
		}
	}
}

// screen is the pane with its colors and attributes; scrollback is everything above it as well.
func (r *uiViewRun) screen() string {
	return r.clean(r.tmux("capture-pane", "-t", r.session, "-p", "-e"))
}
func (r *uiViewRun) scrollback() string {
	return r.clean(r.tmux("capture-pane", "-t", r.session, "-p", "-e", "-S", "-", "-E", "-"))
}

func (r *uiViewRun) tmux(args ...string) string {
	r.t.Helper()
	out, err := tmuxCommand(args...).CombinedOutput()
	if err != nil {
		r.t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// record starts writing the bytes pig writes to the terminal to a file; recorded stops and returns them.
func (r *uiViewRun) record() {
	r.t.Helper()
	r.pipe = filepath.Join(r.t.TempDir(), "pane.raw")
	r.tmux("pipe-pane", "-o", "-t", r.session, "cat > '"+r.pipe+"'")
}

func (r *uiViewRun) recorded() []byte {
	r.t.Helper()
	r.tmux("pipe-pane", "-t", r.session)
	time.Sleep(300 * time.Millisecond)
	data, err := os.ReadFile(r.pipe)
	if err != nil {
		r.t.Fatal(err)
	}
	return data
}

var uiViewVolatile = regexp.MustCompile(`"(id|parentId|timestamp|responseId|cwd)":("[^"]*"|[0-9]+)`)

// sessionFile is the session's entries without what differs between any two runs: ids, timestamps and the directory.
func (r *uiViewRun) sessionFile() string {
	r.t.Helper()
	var files []string
	_ = filepath.WalkDir(filepath.Join(r.home, "agent", "sessions"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if len(files) != 1 {
		r.t.Fatalf("the run wrote %d session files, want 1", len(files))
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var entry map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			r.t.Fatalf("session line %q: %v", line, err)
		}
		out = append(out, uiViewVolatile.ReplaceAllString(line, `"$1":"-"`))
	}
	return r.clean(strings.Join(out, "\n"))
}

// uiViewScript is the keys of one run up to the end of the mounts. With the fixture's mounts off the same keys reach the
// editor and the fixture's commands do nothing.
func uiViewScript(r *uiViewRun, mounted bool, end string) {
	t := r.t
	t.Helper()
	expect := func(needles ...string) {
		t.Helper()
		if mounted {
			r.settle(needles...)
		} else {
			r.settle()
		}
	}
	r.send("/vmount\r")
	expect("fixture widget row", "fixture panel", "Alpha item", "Beta item")
	// The panel asked for the focus: Down and Enter go to its list and raise the intent, whose handler redraws it.
	r.sendKey("Down")
	r.sendKey("Enter")
	expect("picked beta")
	// Escape returns the focus to the editor, so the next command is typed there.
	r.sendKey("Escape")
	r.settle()
	r.send("/vupdate\r")
	expect("fixture widget updated", "picked beta")
	r.send("/vhide\r")
	if mounted {
		r.gone("fixture widget updated", "picked beta", "Alpha item")
	} else {
		r.settle()
	}
	r.send("/vshow\r")
	expect("fixture widget updated", "picked beta", "Alpha item")
	r.send("/" + end + "\r")
	if mounted {
		r.gone("fixture widget", "picked beta", "Alpha item", "viewfx")
	} else {
		r.settle()
	}
}

// uiViewDiff lists the first lines that differ between what a mounted run and a never-mounted run showed.
func uiViewDiff(mounted, never string) string {
	a, b := strings.Split(mounted, "\n"), strings.Split(never, "\n")
	var out []string
	out = append(out, "lines: mounted and ended "+strconv.Itoa(len(a))+", never mounted "+strconv.Itoa(len(b)))
	for i := 0; i < max(len(a), len(b)) && len(out) < 13; i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			out = append(out, strconv.Itoa(i)+" mounted: "+strconv.Quote(x), strconv.Itoa(i)+" never:   "+strconv.Quote(y))
		}
	}
	return strings.Join(out, "\n")
}

type uiViewAfter struct {
	screen, typed, answered, scrollback, session string
	raw                                          []byte
}

// uiViewAfterwards is what a run shows from the end of the mounts on: the screen, the bytes written while a line is
// typed and cleared, the screen and scrollback after a prompt was answered, and the session file.
func uiViewAfterwards(r *uiViewRun) uiViewAfter {
	r.t.Helper()
	var a uiViewAfter
	a.screen = r.screen()
	r.record()
	for _, key := range []string{"h", "e", "l", "l", "o"} {
		r.send(key)
		r.settle()
	}
	a.typed = r.screen()
	r.sendKey("C-c")
	r.settle()
	a.raw = r.recorded()
	r.send("What is 20+22?\r")
	r.settle("42")
	a.answered = r.screen()
	a.scrollback = r.scrollback()
	a.session = r.sessionFile()
	return a
}

// A session in which an extension mounted views, updated one, took an intent from a key, hid, showed and ended them is,
// from then on, the session that never mounted: the same screen with its attributes, the same bytes for the same keys,
// the same scrollback and the same session file. In both TUI modes, after a remove and after a finish.
func TestUIViewMountThenUnmountIsTheSessionThatNeverMounted(t *testing.T) {
	for _, mode := range []string{"fullscreen", "regular"} {
		for _, end := range []string{"vremove", "vfinish"} {
			t.Run(mode+"/"+end, func(t *testing.T) {
				mountedRun := startUIView(t, "on.yaml", mode, true)
				uiViewScript(mountedRun, true, end)
				mounted := uiViewAfterwards(mountedRun)

				neverRun := startUIView(t, "on.yaml", mode, false)
				uiViewScript(neverRun, false, end)
				never := uiViewAfterwards(neverRun)

				controlRun := startUIView(t, "on.yaml", mode, false)
				uiViewScript(controlRun, false, end)
				control := uiViewAfterwards(controlRun)

				for _, c := range []struct{ what, got, want string }{
					{"the screen after the mounts ended", mounted.screen, never.screen},
					{"the screen after typing", mounted.typed, never.typed},
					{"the screen after the answer", mounted.answered, never.answered},
					{"the scrollback", mounted.scrollback, never.scrollback},
					{"the session file", mounted.session, never.session},
				} {
					if c.got != c.want {
						t.Errorf("%s differs:\n%s", c.what, uiViewDiff(c.got, c.want))
					}
					if strings.TrimSpace(c.want) == "" {
						t.Errorf("%s is empty: the comparison proves nothing", c.what)
					}
				}
				if !strings.Contains(never.session, "20+22") {
					t.Errorf("the session file lacks the prompt:\n%s", never.session)
				}
				if !bytes.Equal(never.raw, control.raw) {
					t.Logf("two sessions that never mounted wrote different bytes for the same keys (%d and %d): the byte comparison is not stable here", len(never.raw), len(control.raw))
				} else if !bytes.Equal(mounted.raw, never.raw) {
					t.Errorf("the bytes written for the same keys differ.\n--- mounted and ended ---\n%q\n--- never mounted ---\n%q", mounted.raw, never.raw)
				} else if len(never.raw) == 0 {
					t.Error("no bytes were recorded: the comparison proves nothing")
				}
			})
		}
	}
}

// Stock PiG, and a Piglet that does not select the client, attach none: the fixture's mount fails unsupported and
// nothing is drawn. The Piglet that selects it draws the mount.
func TestUIViewClientIsSelectedByThePiglet(t *testing.T) {
	off := startUIView(t, "off.yaml", "fullscreen", true)
	off.send("/vmount\r")
	pane := off.settle("viewfx vmount: unsupported")
	if strings.Contains(pane, "fixture widget row") || strings.Contains(pane, "fixture panel") {
		t.Fatalf("a Piglet without viewClient drew a mount:\n%s", pane)
	}
	on := startUIView(t, "on.yaml", "fullscreen", true)
	on.send("/vmount\r")
	pane = on.settle("fixture widget row", "fixture panel", "Alpha item")
	if strings.Contains(pane, "unsupported") {
		t.Fatalf("the Piglet that selects the terminal client refused the mount:\n%s", pane)
	}
}

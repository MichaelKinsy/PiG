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

// pig additive (D614): the terminal view client in a real interactive session. A Go SDK fixture
// (testdata/ui-view/fixture), which pig builds and runs as a child process, mounts a panel and a widget, and the test
// drives them with keys through tmux.

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
	return startUIViewAt(t, t.TempDir(), piglet, mode, mounts)
}

// startUIViewOn is startUIView with what the fixture does when it is told a client's abilities
// (PIG_UI_VIEW_FIXTURE_ABILITIES: the slot it mounts a view in, "listen", or "" for no handler). With fromStart the
// bytes written to the pane are recorded from before pig starts.
func startUIViewOn(t *testing.T, piglet, mode string, mounts bool, arrival string, fromStart bool) *uiViewRun {
	t.Helper()
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	return startUIViewSession(t, t.TempDir(), "", []string{"--piglet", filepath.Join(root, "test", "integration", "testdata", "ui-view", piglet)}, mode, mounts, arrival, fromStart)
}

// startUIViewAt is startUIView with the run's home at home.
func startUIViewAt(t *testing.T, home, piglet, mode string, mounts bool) *uiViewRun {
	t.Helper()
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	return startUIViewPiglet(t, home, filepath.Join(root, "test", "integration", "testdata", "ui-view", piglet), mode, mounts)
}

// startUIViewPiglet is startUIViewAt with the Piglet's file named by its path.
func startUIViewPiglet(t *testing.T, home, piglet, mode string, mounts bool) *uiViewRun {
	t.Helper()
	return startUIViewSession(t, home, "", []string{"--piglet", piglet}, mode, mounts, "", false)
}

// startUIViewBinary is startUIViewPiglet for a Piglet Binary, which carries its Piglet: the session runs the Binary in the pig's place.
func startUIViewBinary(t *testing.T, home, binary, mode string) *uiViewRun {
	t.Helper()
	return startUIViewSession(t, home, binary, nil, mode, true, "", false)
}

// startUIViewSession starts a view test's session under tmux with its home at home: the pig the tests built with args, or the executable binary when it names one.
// arrival is what the fixture does when it is told a client's abilities (see startUIViewOn); with fromStart the bytes
// written to the pane are recorded from before the session starts.
func startUIViewSession(t *testing.T, home, binary string, args []string, mode string, mounts bool, arrival string, fromStart bool) *uiViewRun {
	t.Helper()
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
	r := &uiViewRun{harness: h, home: home, cwd: cwd}
	if fromStart {
		r.record()
	}
	args = append(args, "--model", "test-faux/faux-1")
	env := []string{"PIG_HOME=" + home, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_UI_VIEW_FIXTURE_MOUNTS=" + flag, "PIG_UI_VIEW_FIXTURE_ABILITIES=" + arrival}
	if binary == "" {
		h.startArgsAt(args, cwd, env...)
	} else {
		h.startBinaryArgsAt(binary, args, cwd, env...)
	}
	// The first start builds the fixture.
	h.expectContains(4*time.Minute, interactiveReadyMarker)
	return r
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

// uiViewEscape is a control sequence of the terminal: what is left without them is the text that was written.
var uiViewEscape = regexp.MustCompile(`\x1b\[[0-9;:<=>?]*[ -/]*[@-~]`)

var uiViewVolatile = regexp.MustCompile(`"(id|parentId|timestamp|responseId|cwd|durationMs)":("[^"]*"|[0-9]+)`)

// sessionFile is the session's entries without what differs between any two runs: ids, timestamps, the measured duration
// of a response and the directory.
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

// uiViewBytesDiff names the first offset at which two recordings differ and shows the bytes of each around it.
func uiViewBytesDiff(a, b []byte) string {
	at := 0
	for at < len(a) && at < len(b) && a[at] == b[at] {
		at++
	}
	window := func(s []byte) string {
		return strconv.Quote(string(s[min(max(at-40, 0), len(s)):min(at+80, len(s))]))
	}
	return "first difference at byte " + strconv.Itoa(at) + " of " + strconv.Itoa(len(a)) + " and " + strconv.Itoa(len(b)) + "\n" + window(a) + "\n" + window(b)
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

				uiViewIsNeverMounted(t, "mounted and ended", mounted, never, control)
			})
		}
	}
}

// uiViewIsNeverMounted fails unless the run got (named who) shows what the never-mounted run shows. control is a second
// never-mounted run: where the two differ in the bytes they wrote, the byte comparison is unstable and fails as such.
func uiViewIsNeverMounted(t *testing.T, who string, got, never, control uiViewAfter) {
	t.Helper()
	for _, c := range []struct{ what, got, want string }{
		{"the screen after the mounts ended", got.screen, never.screen},
		{"the screen after typing", got.typed, never.typed},
		{"the screen after the answer", got.answered, never.answered},
		{"the scrollback", got.scrollback, never.scrollback},
		{"the session file", got.session, never.session},
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
	switch {
	case !bytes.Equal(never.raw, control.raw):
		t.Errorf("two never-mounted sessions wrote different bytes for the same keys: the comparison is unstable: %s", uiViewBytesDiff(never.raw, control.raw))
	case !bytes.Equal(got.raw, never.raw):
		t.Errorf("the bytes written for the same keys differ.\n--- %s ---\n%q\n--- never mounted ---\n%q", who, got.raw, never.raw)
	case len(never.raw) == 0:
		t.Error("no bytes were recorded: the comparison proves nothing")
	}
}

// An extension that mounts a view when it is told the abilities of the client that attached (OnViewAbilities) is drawn
// once: the view's row is once in the text pig wrote from its start to the end of a typed line, and once on the screen,
// in a panel and in a widget slot. An extension that takes the same event and mounts nothing leaves the session that
// never mounted. In both TUI modes.
func TestUIViewMountOnTheAbilitiesEvent(t *testing.T) {
	const row = "fixture arrival view"
	for _, mode := range []string{"fullscreen", "regular"} {
		t.Run(mode, func(t *testing.T) {
			for _, slot := range []string{"panel", "widget-above"} {
				arrived := startUIViewOn(t, "on.yaml", mode, false, slot, true)
				arrived.settle(row)
				for _, key := range []string{"h", "e", "l", "l", "o"} {
					arrived.send(key)
					arrived.settle()
				}
				pane := arrived.settle("hello")
				arrived.sendKey("C-c")
				arrived.settle()
				for _, c := range []struct{ where, text string }{
					{"in what pig wrote from its start to the end of a typed line", uiViewEscape.ReplaceAllString(string(arrived.recorded()), "")},
					{"on the screen", pane},
					{"in the scrollback", uiViewEscape.ReplaceAllString(arrived.scrollback(), "")},
				} {
					if n := strings.Count(c.text, row); n != 1 {
						t.Errorf("%s: the view mounted on the abilities event is %s %d times, want 1:\n%q", slot, c.where, n, c.text)
					}
				}
				if strings.Contains(pane, "viewfx") {
					t.Errorf("%s: the fixture reported an error:\n%s", slot, pane)
				}
			}

			listeningRun := startUIViewOn(t, "on.yaml", mode, false, "listen", false)
			uiViewScript(listeningRun, false, "vremove")
			listening := uiViewAfterwards(listeningRun)

			neverRun := startUIView(t, "on.yaml", mode, false)
			uiViewScript(neverRun, false, "vremove")
			never := uiViewAfterwards(neverRun)

			controlRun := startUIView(t, "on.yaml", mode, false)
			uiViewScript(controlRun, false, "vremove")
			control := uiViewAfterwards(controlRun)

			uiViewIsNeverMounted(t, "took the abilities event", listening, never, control)
		})
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

// Titled panels are tabs in a real session: one row of titles, the front tab's view alone, F6 from tab to tab, Enter on
// a hidden tab opens it through its whenHidden intent, and a focus request made while the editor is empty moves the
// keys into the view. In both TUI modes.
func TestUIViewPanelsAreTabs(t *testing.T) {
	for _, mode := range []string{"fullscreen", "regular"} {
		t.Run(mode, func(t *testing.T) {
			r := startUIView(t, "on.yaml", mode, true)
			absent := func(pane string, needles ...string) {
				t.Helper()
				for _, needle := range needles {
					if strings.Contains(pane, needle) {
						t.Fatalf("%q is on the screen:\n%s", needle, pane)
					}
				}
			}
			r.send("/vtabs\r")
			absent(r.settle("Mail", "Calendar", "(Notes)", "cal rows"), "mail rows", "notes rows", "[Mail]", "[Calendar]")
			r.sendKey("F6")
			absent(r.settle("[Mail]", "mail rows"), "cal rows", "notes rows")
			r.sendKey("F6")
			absent(r.settle("[Calendar]", "cal rows"), "mail rows", "notes rows")
			r.sendKey("F6")
			absent(r.settle("[(Notes)]", "enter opens", "cal rows"), "notes rows")
			r.sendKey("Enter")
			absent(r.settle("[Notes]", "notes rows"), "cal rows", "(Notes)", "enter opens")
			// The opened tab holds the focus: its list takes the keys.
			r.sendKey("Down")
			r.sendKey("Enter")
			r.settle("notes picked beta")
			r.sendKey("Escape")
			r.settle()
			// With the editor empty, the second tab's request for the focus is granted.
			r.send("/vfocus\r")
			absent(r.settle("[Calendar]", "cal asked"), "notes picked beta")
			r.sendKey("Down")
			r.sendKey("Enter")
			absent(r.settle("cal picked beta"), "viewfx vfocus", "refused")
		})
	}
}

// A mount's key bindings in a real session: the bound key is the editor's until the user enters the mount with F6; then
// the hint row names it, the key raises the intent with the binding's argument, or {} without one, and the extension's
// handler shows what it was given. Escape leaves the mount and the hint row goes. In both TUI modes.
func TestUIViewBindingsFireFromTheFocusedMount(t *testing.T) {
	for _, mode := range []string{"fullscreen", "regular"} {
		t.Run(mode, func(t *testing.T) {
			r := startUIView(t, "on.yaml", mode, true)
			r.send("/vbind\r")
			if pane := r.settle("bind rows"); strings.Contains(pane, "d Day") {
				t.Fatalf("the hint row is drawn before the mount holds the focus:\n%s", pane)
			}
			r.send("d")
			if pane := r.settle(); strings.Contains(pane, "bound {") {
				t.Fatalf("a binding fired while the editor held the focus:\n%s", pane)
			}
			r.sendKey("BSpace")
			r.sendKey("F6")
			r.settle("d Day", "bind rows")
			r.send("d")
			r.settle(`bound {"n":7}`, "d Day")
			r.send("w")
			r.settle("bound {}", "d Day")
			r.sendKey("Escape")
			r.gone("d Day")
			r.send("d")
			if pane := r.settle(); !strings.Contains(pane, "bound {}") || strings.Contains(pane, `bound {"n":7}`) {
				t.Fatalf("after Escape the bound key changed the panel:\n%s", pane)
			}
		})
	}
}

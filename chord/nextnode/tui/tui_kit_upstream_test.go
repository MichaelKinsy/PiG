package tui

// pi: packages/chord/src/node/surfaces/tui.ts
// pi: packages/chord/src/node/surfaces/served.ts
// pi: packages/chord/src/node/surfaces/current.ts

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	next "github.com/MichaelKinsy/PiG/chord/next/surfaces"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Tests of the terminal launcher (slice 11 §4, decision "Slice 11, P8") with a kit app's terminal surface: it signs in with the
// owner token file or by pairing, runs the app's terminal code in the sandbox with the plugins' scripts, relays its Chord link,
// confirms its actions on its own prompt, applies a plugin edit in place, and ends with its exit code. Run under Node 26.5.1 or
// later; skipped without one. Ports the `a terminal client` describe of packages/chord/test/node/tui/launcher.test.ts, the test of
// packages/chord/test/node/surfaces/tui.test.ts and the whole-sandbox test of packages/chord/test/node/tui/sandbox.test.ts.

// reset is the terminal's modes back to the shell's, the scroll region and autowrap included (Opus E11).
const reset = "\x1b[?2026l\x1b[?1000l\x1b[?1002l\x1b[?1006l\x1b[?1004l\x1b[?2004l\x1b[?25h\x1b7\x1b[r\x1b8\x1b[?7h\x1b[?1049l\x1b[0m"

// untilMarks returns once the plugins' marks satisfy ok; the state's watch wakes it (no polling).
func untilMarks(t testing.TB, a *app.App, what string, ok func(plugins.PluginsState) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(background, 30*time.Second)
	defer cancel()
	watch, err := a.Plugins.Component.State().Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = watch.Stop() }()
	changed := make(chan struct{}, 1)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error {
		select {
		case changed <- struct{}{}:
		default:
		}
		return nil
	})
	for !ok(a.Plugins.Component.Marks()) {
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s (marks %+v)", what, a.Plugins.Component.Marks())
		}
	}
}

func TestTheTerminalSurfaceServesASignedInLauncherItsStartTheBaseBundleAndTheScriptsTheBaseHashToAnyone(t *testing.T) {
	s := tuiApp(t, "plugin v1")
	text, err := os.ReadFile(s.app.OwnerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"authorization": "Bearer " + strings.TrimSpace(string(text))}
	if status, _ := httpDo(t, s.port, "GET", "/_chord/tui/start", nil); status != 401 {
		t.Fatalf("start without a token = %d, want 401", status)
	}
	status, body := httpDo(t, s.port, "GET", "/_chord/tui/start", headers)
	if status != 200 {
		t.Fatalf("start = %d %s", status, body)
	}
	decoded, err := delta.DecodeJson([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	start := decoded.(*delta.JsonObject)
	if start.Value("base") != s.app.Tui.Base() || start.Value("script") != "/_chord/tui/base."+s.app.Tui.Base()+".js" ||
		!regexp.MustCompile(`^/_chord/tui/plugins\.[0-9a-f]{16}\.js$`).MatchString(start.Value("combined").(string)) {
		t.Fatalf("start = %s", body)
	}
	if _, baseBody := httpDo(t, s.port, "GET", "/_chord/tui/base", nil); baseBody != `"`+s.app.Tui.Base()+`"` {
		t.Fatalf("base = %s", baseBody)
	}
	_, base := httpDo(t, s.port, "GET", start.Value("script").(string), headers)
	// A CommonJS script (the sandbox gives it its own `require`), no ES module.
	if !strings.Contains(base, "require(") || regexp.MustCompile(`(?m)^\s*(?:import|export)\b`).MatchString(base) {
		t.Fatalf("the base is no CommonJS script: %.200s", base)
	}
	scripts, err := s.app.Host.Get(background, next.TuiScripts, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer scripts.Release()
	value, err := scripts.Member("set").Value()
	if err != nil {
		t.Fatal(err)
	}
	set := next.ScriptSetOf(value)
	if len(set.Plugins) == 0 {
		built, err := s.app.Plugins.Component.Build([]string{"@test/lines"}, background)
		if err != nil {
			t.Fatal(err)
		}
		entries, _ := os.ReadDir(filepath.Join(s.root, "lines"))
		for _, entry := range entries {
			t.Logf("file %s", entry.Name())
		}
		t.Fatalf("the set has no plugin script; the build: %+v; discovery: %+v", built.Facets, s.app.Plugins.Component.Discovery())
	}
	entry := set.Plugins[0]
	if entry.URL != "/_chord/tui/plugin/@test~lines."+entry.Digest+".js" {
		t.Fatalf("url = %s", entry.URL)
	}
	_, script := httpDo(t, s.port, "GET", entry.URL, headers)
	if !strings.HasPrefix(script, `chordFacet("@test/lines","`+entry.Digest+`",`) {
		t.Fatalf("script = %.120s", script)
	}
	_, combined := httpDo(t, s.port, "GET", start.Value("combined").(string), headers)
	if want := regexp.MustCompile(`\n//# sourceMappingURL=data:[^\n]*`).ReplaceAllString(script, ""); combined != want {
		t.Fatalf("the combined file is not the script without its source map")
	}
	// Terminal facets are Node builds: an asset import fails the build, so the surface serves none.
	writePlugin(t, s.root, "pictured", "@test/pictured", map[string]string{
		"tui.ts":   "import icon from \"./icon.svg?url\";\nexport default { setup: () => icon };",
		"icon.svg": "<svg/>",
	})
	// The watcher noticed the new plugin once the plugin set is marked: then the build discovers it.
	untilMarks(t, s.app, "the plugin set's mark", func(state plugins.PluginsState) bool { return state.Set })
	built, err := s.app.Plugins.Component.Build([]string{"@test/pictured"}, background)
	if err != nil {
		t.Fatal(err)
	}
	for _, facet := range built.Facets {
		if facet.Plugin == "@test/pictured" {
			if facet.Kind != model.BuiltFacetFailed || len(facet.Errors) == 0 || !strings.Contains(facet.Errors[0], "assets by URL are for browser builds") {
				t.Fatalf("facet = %+v, want a failed build refusing assets by URL", facet)
			}
			return
		}
	}
	t.Fatal("no build of the pictured plugin")
}

func TestATerminalClientRunsTheAppsTerminalCodeWithThePluginsConfirmsItsActionsAppliesAnEditInPlaceExits(t *testing.T) {
	s := tuiApp(t, "plugin v1")
	l := launchWithToken(t, s)
	l.waitFor(`ready 80x24 \x1b\[38;2;76;154;255mthemed\x1b\[39m`)
	l.waitFor(`lines: \["plugin v1 tried=\d+ escaped=none"\]`)
	// Copying: the launcher's prompt, then OSC 52 from the launcher itself.
	l.typeKeys("c")
	l.waitFor(`Copy these 15 characters \(1 line\)\? \[y/N\]`)
	if !strings.Contains(l.output(), "  hello clipboard") {
		t.Fatalf("the text was not shown: %q", l.output())
	}
	l.answer("y")
	l.waitFor(`\x1b\]52;c;aGVsbG8gY2xpcGJvYXJk\x07`)
	l.waitFor(`copied true`)
	l.typeKeys("o")
	l.waitFor(`Open example\.com in the browser\? \[y/N\]`)
	l.answer("n")
	l.waitFor(`opened false`)
	if strings.Contains(l.output(), "(opener") {
		t.Fatalf("the opener ran: %q", l.output())
	}
	// An edit of the terminal plugin applies in place: the sandbox fetches the new script through the launcher.
	if err := os.WriteFile(filepath.Join(s.root, "lines", "tui.ts"), []byte(linesPlugin(t, "plugin v2")["tui.ts"]), 0o644); err != nil {
		t.Fatal(err)
	}
	untilMarks(t, s.app, "the edit's dirty mark", func(state plugins.PluginsState) bool {
		return slices.Contains(state.Dirty, "@test/lines")
	})
	result, err := s.app.ApplyChanges(background)
	if err != nil {
		t.Fatal(err)
	}
	var steps []string
	for _, step := range result.Steps {
		steps = append(steps, step.Name)
	}
	if strings.Join(steps, ",") != "server,tui" {
		t.Fatalf("steps = %v, want server, tui", steps)
	}
	l.waitFor(`lines: \["plugin v2 tried=\d+ escaped=none"\]`)
	if strings.Contains(l.output(), "ready 80x24 \x1b[38;2;76;154;255mthemed\x1b[39m\r\nready") {
		t.Fatalf("the sandbox restarted: %q", l.output())
	}
	l.typeKeys("q")
	if code := l.exited(); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	// The terminal's modes back to the shell's.
	if !strings.HasSuffix(l.output(), reset) {
		t.Fatalf("the output does not end with the reset: %q", l.output())
	}
}

func TestATerminalClientHoldsTheSandboxsFramesWhileItsPromptIsOpenAndRefusesASecondRequestMeanwhileFableH1OpusM1(t *testing.T) {
	s := tuiApp(t, "plugin v1")
	l := launchWithToken(t, s)
	l.waitFor(`lines: \[`)
	l.typeKeys("x")
	l.waitFor(`Attach this file\? \[y/N\]`)
	time.Sleep(300 * time.Millisecond)
	// The fake prompt the sandbox wrote at once is not on the terminal while the real one waits.
	if strings.Contains(l.output(), "FAKE") {
		t.Fatalf("the sandbox's frame was not held: %q", l.output())
	}
	l.answer("n")
	l.waitFor(`attached false`)
	if strings.Index(l.output(), "FAKE") <= strings.Index(l.output(), "Attach this file?") {
		t.Fatalf("the held frame was not written after the prompt: %q", l.output())
	}
	l.typeKeys("z")
	l.waitFor(`Copy these 3 characters`)
	l.answer("n")
	l.waitFor(`copies: done false / refused terminal: a prompt is open`)
	l.typeKeys("q")
	if code := l.exited(); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
}

func TestATerminalClientDrawsItsPromptInItsOwnColoursAndIgnoresKeysTypedBeforeItAppearedOpusE4(t *testing.T) {
	s := tuiApp(t, "plugin v1")
	l := launchWithToken(t, s)
	l.waitFor(`lines: \[`)
	// The sandbox leaves concealed black on black, asks, and a "y" typed for it arrives with the prompt.
	l.typeKeys("h")
	time.Sleep(30 * time.Millisecond)
	l.typeKeys("y")
	l.waitFor(`Copy these 6 characters`)
	prompt := l.output()[strings.LastIndex(l.output(), "\x1b[?2026l\x1b[s"):]
	if !regexp.MustCompile(`^\x1b\[\?2026l\x1b\[s\x1b\[\d+;1H\x1b\[0;30;47m\x1b\[2K {3}hidden `).MatchString(prompt) {
		t.Fatalf("prompt = %q", prompt)
	}
	time.Sleep(400 * time.Millisecond)
	if strings.Contains(l.output(), "hidden copied") {
		t.Fatalf("a key typed before the prompt answered it: %q", l.output())
	}
	l.answer("n")
	l.waitFor(`hidden copied false`)
	l.typeKeys("q")
	if code := l.exited(); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
}

func TestATerminalClientSurvivesWhatTheSandboxSendsToBreakItAndGivesTheTerminalBackWhenTheSandboxDiesOpusE3(t *testing.T) {
	s := tuiApp(t, "plugin v1")
	l := launchWithToken(t, s)
	l.waitFor(`lines: \[`)
	l.typeKeys("w")
	l.waitFor(`survived: huge write RangeError, huge bytes RangeError`)
	// Still running: a request still answered.
	l.typeKeys("o")
	l.waitFor(`Open example\.com in the browser\?`)
	l.answer("n")
	l.waitFor(`opened false`)
	// The sandbox's process ends: the client ends with 1, the terminal's modes reset.
	l.typeKeys("k")
	if code := l.exited(); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.HasSuffix(l.output(), reset) {
		t.Fatalf("the output does not end with the reset: %q", l.output())
	}
}

// floodLine is the line the sandbox prints after twelve requests at once: how many were answered and, when some were refused, how
// many (Pi's fixture prints each distinct outcome with its count, so a flood that was refused nothing has no second clause).
var floodLine = regexp.MustCompile(`^requests: (\d+) answered(?:, (\d+) terminal: too many requests at once)?\r\n$`)

// floodOutcome reads floodLine's counts; ok is false for any other line.
func floodOutcome(line string) (answered, refused int, ok bool) {
	found := floodLine.FindStringSubmatch(line)
	if found == nil {
		return 0, 0, false
	}
	answered, _ = strconv.Atoi(found[1])
	if found[2] != "" {
		refused, _ = strconv.Atoi(found[2])
	}
	return answered, refused, true
}

// The twelve requests reach the launcher as the sandbox's writes do: when the child is descheduled between them (a loaded host), the
// first are answered before the last arrive, and all twelve are answered with none refused. That is the outcome the flood test's own
// comment allows ("one answered before the next arrives counts as answered"), and its line has no refusal clause. The launcher's
// own limit (the ninth is refused while eight are being answered) is asserted without the timing in
// TestAtMostEightRequestsAreAnsweredAtOnceAndTheNextIsRefusedUntilOneEnds.
func TestTheFloodOutcomeIsReadWithAndWithoutARefusal(t *testing.T) {
	for _, c := range []struct {
		line              string
		answered, refused int
		ok                bool
	}{
		{"requests: 8 answered, 4 terminal: too many requests at once\r\n", 8, 4, true},
		{"requests: 12 answered\r\n", 12, 0, true},
		{"requests: 8 answered, 4 some other refusal\r\n", 0, 0, false},
		{"requests: 12 refused\r\n", 0, 0, false},
	} {
		answered, refused, ok := floodOutcome(c.line)
		if answered != c.answered || refused != c.refused || ok != c.ok {
			t.Errorf("floodOutcome(%q) = %d, %d, %v; want %d, %d, %v", c.line, answered, refused, ok, c.answered, c.refused, c.ok)
		}
	}
}

func TestATerminalClientAnswersEightRequestsAtOnceRefusesMoreAndIgnoresSIGUSR1OpusE10E8(t *testing.T) {
	s := tuiApp(t, "plugin v1")
	l := launchWithToken(t, s)
	l.waitFor(`lines: \[`)
	// Twelve sent at once: the first eight answered, the rest refused (one answered before the next arrives counts as answered).
	flood := func() {
		mark := len(l.output())
		l.typeKeys("f")
		line := l.waitFor(`requests: [^\r]*\r\n`, mark)[0]
		answered, refused, ok := floodOutcome(line)
		if !ok || answered < 8 || answered+refused != 12 {
			t.Fatalf("line = %q (answered %d, refused %d)", line, answered, refused)
		}
	}
	flood()
	// A signal the sandbox may send its launcher opens no inspector (here: does not end the process).
	signalInspector(t)
	if strings.Contains(l.output(), "Debugger listening") {
		t.Fatalf("an inspector opened: %q", l.output())
	}
	flood()
	l.typeKeys("q")
	if code := l.exited(); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
}

func TestATerminalClientEndsOnCtrlCWhileItsPromptIsOpenWhateverTheSandboxAsksOpusE11(t *testing.T) {
	s := tuiApp(t, "plugin v1")
	l := launchWithToken(t, s)
	l.waitFor(`lines: \[`)
	l.typeKeys("c")
	l.waitFor(`Copy these 15 characters`)
	l.typeKeys("\x03")
	if code := l.exited(); code != 130 {
		t.Fatalf("exit code = %d, want 130", code)
	}
	if !strings.HasSuffix(l.output(), reset) {
		t.Fatalf("the output does not end with the reset: %q", l.output())
	}
}

func TestATerminalClientEndsOnThreeCtrlCWithinASecondThoughTheSandboxIgnoresThemOpusR2(t *testing.T) {
	s := tuiApp(t, "plugin v1")
	l := launchWithToken(t, s)
	l.waitFor(`lines: \[`)
	// The fixture's terminal code does nothing with Ctrl-C. One, then two more over a second later: still running.
	l.typeKeys("\x03")
	time.Sleep(1100 * time.Millisecond)
	l.typeKeys("\x03")
	time.Sleep(50 * time.Millisecond)
	l.typeKeys("\x03")
	time.Sleep(300 * time.Millisecond)
	l.typeKeys("o")
	l.waitFor(`Open example\.com in the browser\?`)
	l.answer("n")
	l.waitFor(`opened false`)
	// Three within a second: the client ends, the terminal reset.
	for range 3 {
		l.typeKeys("\x03")
		time.Sleep(50 * time.Millisecond)
	}
	if code := l.exited(); code != 130 {
		t.Fatalf("exit code = %d, want 130", code)
	}
	if !strings.HasSuffix(l.output(), reset) {
		t.Fatalf("the output does not end with the reset: %q", l.output())
	}
}

func TestATerminalClientPairsWithTheServerTheCodeShownApprovedByTheOwnerThenTheClientRuns(t *testing.T) {
	pollInterval = 100 * time.Millisecond
	t.Cleanup(func() { pollInterval = 2000 * time.Millisecond })
	s := tuiApp(t, "plugin v1")
	l := launch(t, s.port, TerminalCredentials{Kind: CredentialsPairing, Label: "test terminal"})
	code := l.waitFor(`Pairing code ([A-Z0-9]{4}-[A-Z0-9]{4}): approve it at `)[1]
	signIns, err := s.app.Host.Get(background, app.SignIns, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer signIns.Release()
	issued, err := signIns.Call("issue", delta.JsonObjectOf("principal", delta.JsonObjectOf("kind", "owner"), "label", "o", "kind", "device"), background)
	if err != nil {
		t.Fatal(err)
	}
	token := issued.(*delta.JsonObject).Value("token").(string)
	if status, body := httpDo(t, s.port, "POST", "/_chord/pair/"+code, map[string]string{"authorization": "Bearer " + token}); status != 200 {
		t.Fatalf("approve = %d %s", status, body)
	}
	l.waitFor(`lines: \["plugin v1 tried=\d+ escaped=none"\]`)
	list, err := signIns.Member("list").Value()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range list.([]any) {
		found = found || item.(*delta.JsonObject).Value("label") == "test terminal"
	}
	if !found {
		t.Fatalf("no sign-in labelled the test terminal: %v", list)
	}
	l.typeKeys("q")
	if code := l.exited(); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
}

func TestTheSandboxClosesEveryEscapeToTheAppsCodeAndToPluginsOnARealTerminalFableC1OpusH1E1E2(t *testing.T) {
	s := tuiApp(t, "plugin v1")
	l := launchWithToken(t, s)
	// A plugin's own attempts, at its evaluation: none got through.
	tried, _ := strconv.Atoi(l.waitFor(`lines: \["plugin v1 tried=(\d+) escaped=none"\]`)[1])
	if tried <= 80 {
		t.Fatalf("tried = %d, want over 80", tried)
	}
	l.typeKeys("p")
	text := l.waitFor(`probes: (\{[^\r]*\})`)[1]
	decoded, err := delta.DecodeJson([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	found := decoded.(*delta.JsonObject)
	if found.Len() <= 80 {
		t.Fatalf("probes = %d, want over 80", found.Len())
	}
	for _, key := range found.Keys() {
		if found.Value(key) == "ok" {
			t.Errorf("escape %q got through", key)
		}
	}
	for key, want := range map[string]string{
		"getBuiltinModule tty":                     "node:tty: unavailable in the terminal's sandbox",
		"require sqlite":                           "node:sqlite: unavailable in the terminal's sandbox",
		"Set.prototype.has patched":                "node:tty: unavailable in the terminal's sandbox",
		"String.prototype.slice patched":           "node:events: unavailable in the terminal's sandbox",
		"emitter":                                  "not reached",
		"a getter on Object.prototype":             "Cannot define property noDeprecation, object is not extensible",
		"a getter on EventEmitter.prototype":       "Cannot define property noDeprecation, object is not extensible",
		"a prototype under EventEmitter.prototype": "#<EventEmitter> is not extensible",
		"getter": "Cannot define property noDeprecation, object is not extensible",
		"import": "ERR_VM_DYNAMIC_IMPORT_CALLBACK_MISSING",
		"env":    "empty",
	} {
		if got := found.Value(key); got != want {
			t.Errorf("probe %q = %v, want %q", key, got, want)
		}
	}
	if got, _ := found.Value("process.binding('tty_wrap')").(string); !strings.HasSuffix(got, "is not a function") {
		t.Errorf("process.binding('tty_wrap') = %q", got)
	}
	// The process the sandbox's code sees: exactly the allowed properties, a frozen plain object (a new property fails here until
	// it is judged and allowed).
	l.typeKeys("s")
	shape, err := delta.DecodeJson([]byte(l.waitFor(`process: (\{[^\r]*\})`)[1]))
	if err != nil {
		t.Fatal(err)
	}
	own := shape.(*delta.JsonObject).Value("own").([]any)
	var names []string
	for _, name := range own {
		names = append(names, name.(string))
	}
	want := append([]string(nil), SandboxProcess...)
	sortStrings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") || shape.(*delta.JsonObject).Value("plain") != true || shape.(*delta.JsonObject).Value("frozen") != true {
		t.Errorf("process shape = %v, want own %v, plain, frozen", shape, want)
	}
	l.typeKeys("q")
	if code := l.exited(); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	if strings.Contains(l.output(), "ESCAPE-MARK") {
		t.Fatalf("the output holds the escape mark")
	}
}

func sortStrings(values []string) { slices.Sort(values) }

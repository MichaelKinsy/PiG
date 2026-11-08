package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

// The session selector contracts (coding-agent/session-selector/*) pin one obligation per case: the keys run through pinned Pi's
// SessionSelectorComponent and through PiG's, every frame and callback event must be equal, and the case's own check states the rule it exercises in
// terms of the rendered rows so that a case cannot pass by both sides doing nothing.

type sessionContractProbe struct {
	sessionOracleProbe
	// GlobalBindings are the process-wide bindings; null means the same as Bindings, which are the manager passed to the component.
	GlobalBindings map[string][]string `json:"globalBindings"`
	Pending        *sessionPending     `json:"pending"`
}

// sessionPending holds the current scope's load open: the loader reports Progress and then waits until key ReleaseBefore is about to run.
type sessionPending struct {
	Progress      []sessionProgress `json:"progress"`
	ReleaseBefore int               `json:"releaseBefore"`
}

type sessionProgress struct {
	Loaded  int `json:"loaded"`
	Total   int `json:"total"`
	Partial int `json:"partial"` // the first Partial sessions of the scope, or 0 for none
}

type sessionContractCase struct {
	name         string
	fixture      string // "small", "big", "unnamed", "one", "empty", "empty-current"
	currentPath  string
	noRename     bool
	zeroWidth    bool // run at width 0 instead of the default 100
	pending      *sessionPending
	bindings     map[string][]string
	global       map[string][]string // non-nil: process-wide bindings differing from bindings
	width        int
	theme        string
	keys         []string
	check        func(t *testing.T, tr sessionContractTrace)
	renameResult func(path, name string) error
}

type sessionContractTrace struct {
	Steps  [][]string // Steps[i] are the events after key i
	Frames [][]string // Frames[0] is the first frame
	Keys   []string
}

func (tr sessionContractTrace) last() []string { return tr.Frames[len(tr.Frames)-1] }
func (tr sessionContractTrace) events() []string {
	if len(tr.Steps) == 0 {
		return nil
	}
	return tr.Steps[len(tr.Steps)-1]
}

var (
	sessionRowTail = regexp.MustCompile(`\d+ (now|\d+(m|h|d|w|mo|y))\s*$`)
	sessionMessage = regexp.MustCompile(`message number \d+|named-\d+|named|elsewhere`)
)

func sessionPlain(line string) string { return treePlain(line) }

func sessionFrameText(frame []string) string {
	plain := make([]string, len(frame))
	for i, line := range frame {
		plain[i] = sessionPlain(line)
	}
	return strings.Join(plain, "\n")
}

// sessionRows are the plain text of the session rows a frame draws.
func sessionRows(frame []string) []string {
	var rows []string
	for _, line := range frame {
		if plain := sessionPlain(line); sessionRowTail.MatchString(plain) {
			rows = append(rows, plain)
		}
	}
	return rows
}

func sessionSelected(frame []string) string {
	for _, line := range frame {
		if plain := sessionPlain(line); strings.HasPrefix(plain, "› ") && sessionRowTail.MatchString(plain) {
			return plain
		}
	}
	return ""
}

func sessionScroll(frame []string) string {
	for _, line := range frame {
		if plain := sessionPlain(line); regexp.MustCompile(`^  \(\d+/\d+\)$`).MatchString(plain) {
			return strings.TrimSpace(plain)
		}
	}
	return ""
}

func sessionContractFixtures() map[string][2][]sessionOracleInfo {
	small, big, all := sessionOracleFixtures()
	unnamed := []sessionOracleInfo{small[0], small[2]}
	stamp := func(offset time.Duration) string {
		return time.Now().Add(-offset).UTC().Format("2006-01-02T15:04:05.000Z")
	}
	// Each age sits in the middle of its bucket (now, m, h, d, w, mo, y) so a few seconds between Pi's run and PiG's cannot move a row to the next one.
	var ages []sessionOracleInfo
	for i, offset := range []time.Duration{30 * time.Second, 5*time.Minute + 30*time.Second, 90 * time.Minute, 5*time.Hour + 30*time.Minute, 84 * time.Hour, 252 * time.Hour, 492 * time.Hour, 45 * 24 * time.Hour, 100 * 24 * time.Hour, 400 * 24 * time.Hour, 800 * 24 * time.Hour} {
		ages = append(ages, sessionOracleInfo{Path: fmt.Sprintf("/work/ages/a%02d.jsonl", i), ID: fmt.Sprintf("age%02d", i), CWD: "/work/proj", Modified: stamp(offset), MessageCount: i * 7, FirstMessage: fmt.Sprintf("age row %d", i), AllMessagesText: fmt.Sprintf("age row %d", i)})
	}
	home, _ := os.UserHomeDir()
	text := []sessionOracleInfo{
		{Path: home + "/s/t00.jsonl", ID: "t0", CWD: home + "/proj", Modified: stamp(time.Hour), MessageCount: 1, FirstMessage: "line one\nline two\ttabbed\x00nul\x1b[31mred\x7fdel", AllMessagesText: "x"},
		{Path: home + "/s/t01.jsonl", ID: "t1", CWD: "", Modified: stamp(2 * time.Hour), MessageCount: 22, FirstMessage: "宽字符 wide text 😀 e\u0301 combining and a very long tail that has to be cut off at narrow terminals", AllMessagesText: "x"},
		{Path: "/elsewhere/t02.jsonl", ID: "t2", CWD: "/elsewhere", Name: "日本語 named 😀 session with a long name that has to be cut off at narrow terminals", Modified: stamp(3 * time.Hour), MessageCount: 333, FirstMessage: "ignored", AllMessagesText: "x"},
		{Path: "/elsewhere/t03.jsonl", ID: "t3", CWD: "/elsewhere/deeper/than/most", Name: "  padded\tname  ", Modified: stamp(4 * time.Hour), MessageCount: 4444, FirstMessage: "ignored", AllMessagesText: "x"},
		{Path: "/elsewhere/t04.jsonl", ID: "t4", CWD: "/x", Modified: stamp(5 * time.Hour), MessageCount: 5, FirstMessage: "", AllMessagesText: ""},
		{Path: "/elsewhere/t05.jsonl", ID: "t5", CWD: "/x", Modified: stamp(6 * time.Hour), MessageCount: 0, FirstMessage: "\n\n\t", AllMessagesText: ""},
	}
	// Threads: a (b, c (d, e (f))), g (h), i with a parent that is not listed.
	thread := func(name, parent string, minutes int) sessionOracleInfo {
		info := sessionOracleInfo{Path: "/work/threads/" + name + ".jsonl", ID: name, CWD: "/work/proj", Modified: stamp(time.Duration(minutes) * time.Hour), MessageCount: minutes, FirstMessage: "thread " + name, AllMessagesText: "thread " + name}
		if parent != "" {
			info.ParentSessionPath = "/work/threads/" + parent + ".jsonl"
		}
		return info
	}
	threads := []sessionOracleInfo{thread("a", "", 40), thread("b", "a", 39), thread("c", "a", 38), thread("d", "c", 37), thread("e", "c", 36), thread("f", "e", 35), thread("g", "", 34), thread("h", "g", 33), thread("i", "missing", 32)}
	return map[string][2][]sessionOracleInfo{
		"ages":          {ages, ages},
		"text":          {text, text},
		"threads":       {threads, threads},
		"small":         {small, all},
		"big":           {big, all},
		"unnamed":       {unnamed, unnamed},
		"one":           {small[:1], small[:1]},
		"empty":         {{}, {}},
		"empty-current": {{}, all},
	}
}

func runSessionContracts(t *testing.T, cases []sessionContractCase) []sessionContractTrace {
	t.Helper()
	fixtures := sessionContractFixtures()
	probes := make([]sessionContractProbe, len(cases))
	for i, c := range cases {
		fx, ok := fixtures[c.fixture]
		if !ok {
			t.Fatalf("case %q: unknown fixture %q", c.name, c.fixture)
		}
		width, theme := c.width, c.theme
		if width == 0 && !c.zeroWidth {
			width = 100
		}
		if theme == "" {
			theme = "dark"
		}
		if c.pending != nil && c.pending.Progress == nil {
			c.pending.Progress = []sessionProgress{}
		}
		probes[i] = sessionContractProbe{
			Theme: theme, Width: width, Current: fx[0], All: fx[1], CurrentPath: c.currentPath, Rename: !c.noRename, Bindings: c.bindings, Keys: append([]string{}, c.keys...),
			GlobalBindings: c.global,
			Pending:        c.pending,
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/session_selector_contract.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []sessionOracleResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(cases) {
		t.Fatalf("Pi answered %d of %d cases", len(expected), len(cases))
	}
	previousTheme, previousCaps, previousKeys := tui.ActiveTheme(), tui.GetCapabilities(), tui.GetTUIKeybindings()
	t.Cleanup(func() {
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme.Name)
		tui.SetTUIKeybindings(previousKeys)
	})
	traces := make([]sessionContractTrace, len(cases))
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			probe := probes[i]
			got, completedAt := replaySessionContract(t, probe, c)
			traces[i] = got
			want := expected[i]
			want.Steps, want.Frames = want.Steps[:completedAt], want.Frames[:completedAt+1]
			if !reflect.DeepEqual(got.Steps, want.Steps) || !reflect.DeepEqual(got.Frames, want.Frames) {
				t.Fatalf("keys=%q differ from Pi:\nsteps = %q\nPi    = %q\n%s", probe.Keys, got.Steps, want.Steps, firstFrameDifference(got.Frames, want.Frames))
			}
			if c.check != nil {
				c.check(t, got)
			}
		})
	}
	return traces
}

func replaySessionContract(t *testing.T, probe sessionContractProbe, c sessionContractCase) (sessionContractTrace, int) {
	t.Helper()
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme(probe.Theme)
	manager := func(bindings map[string][]string) *KeybindingsManager {
		bindingMap := map[string][]KeyID{}
		for action, keys := range bindings {
			bindingMap[action] = []KeyID{}
			for _, key := range keys {
				bindingMap[action] = append(bindingMap[action], KeyID(key))
			}
		}
		km := DefaultKeybindingsManager()
		km.SetUserBindings(bindingMap)
		return km
	}
	option := manager(probe.Bindings)
	global := option
	if probe.GlobalBindings != nil {
		global = manager(probe.GlobalBindings)
	}
	global.syncToTUI()
	var events []string
	var rename func(string, string) error
	if probe.Rename {
		rename = func(path, name string) error {
			events = append(events, "rename:"+path+":"+name)
			if c.renameResult != nil {
				return c.renameResult(path, name)
			}
			return nil
		}
	}
	toGo := func(infos []sessionOracleInfo) []SessionInfo {
		var out []SessionInfo
		for _, s := range infos {
			modified, _ := time.Parse("2006-01-02T15:04:05.000Z", s.Modified)
			out = append(out, SessionInfo{Path: s.Path, ID: s.ID, CWD: s.CWD, Name: s.Name, ParentSessionPath: s.ParentSessionPath, Created: modified, Modified: modified,
				MessageCount: s.MessageCount, FirstMessage: s.FirstMessage, AllMessagesText: s.AllMessagesText})
		}
		return out
	}
	current, all := toGo(probe.Current), toGo(probe.All)
	var selector *SessionSelectorComponent
	var pendingResult chan sessionLoadResult
	released := false
	if probe.Pending == nil {
		selector = newLoadedSessionSelector(func() ([]SessionInfo, error) { return current, nil }, func() ([]SessionInfo, error) { return all, nil }, rename, nil, probe.CurrentPath, option)
	} else {
		pending := probe.Pending
		// The pending load reports its progress synchronously inside the loader, as Pi's async loader does before its first await, and settles when released.
		currentLoader := func(_ *sessionLoad, progress SessionListProgress) <-chan sessionLoadResult {
			pendingResult = make(chan sessionLoadResult, 1)
			for _, p := range pending.Progress {
				var partial []SessionInfo
				if p.Partial > 0 {
					partial = current[:p.Partial]
				}
				progress(p.Loaded, p.Total, partial)
			}
			if released {
				pendingResult <- sessionLoadResult{current, nil}
			}
			return pendingResult
		}
		selector = newSessionSelectorWithLoaders(currentLoader, resolvedSessionLoader(func() ([]SessionInfo, error) { return all, nil }), rename, nil, probe.CurrentPath, option)
		selector.drainLoadUpdates()
	}
	selector.renameSession = rename
	selector.showRenameHint = rename != nil
	// Pi's deleteSessionFile reports a trash success for a path that does not exist.
	selector.deleteSession = func(string) sessionDeleteResult { return sessionDeleteResult{ok: true, method: sessionDeleteTrash} }
	selector.SetFocused(true)
	got := sessionContractTrace{Steps: [][]string{}, Frames: [][]string{piJSONLines(selector.Render(probe.Width))}, Keys: probe.Keys}
	reported := false
	completedAt := len(probe.Keys)
	for keyIndex, key := range probe.Keys {
		if probe.Pending != nil && probe.Pending.ReleaseBefore == keyIndex {
			released = true
			pendingResult <- sessionLoadResult{current, nil}
			selector.drainLoadUpdates()
		}
		selector.HandleInput(key)
		selector.drainLoadUpdates()
		if selector.Done() && !reported {
			if selector.Cancelled() {
				events = append(events, "cancel")
			} else {
				events = append(events, "select:"+selector.SelectedPath())
			}
			reported = true
		}
		got.Steps = append(got.Steps, append([]string{}, events...))
		got.Frames = append(got.Frames, piJSONLines(selector.Render(probe.Width)))
		if selector.Done() {
			completedAt = keyIndex + 1
			break
		}
	}
	return got, completedAt
}

func sessionWantAll(checks ...func(t *testing.T, tr sessionContractTrace)) func(t *testing.T, tr sessionContractTrace) {
	return func(t *testing.T, tr sessionContractTrace) {
		t.Helper()
		for _, c := range checks {
			c(t, tr)
		}
	}
}

func sessionWantSelected(part string) func(t *testing.T, tr sessionContractTrace) {
	return func(t *testing.T, tr sessionContractTrace) {
		t.Helper()
		if row := sessionSelected(tr.last()); !strings.Contains(row, part) {
			t.Errorf("highlighted row = %q, want it to contain %q", row, part)
		}
	}
}

func sessionWantEvents(want ...string) func(t *testing.T, tr sessionContractTrace) {
	return func(t *testing.T, tr sessionContractTrace) {
		t.Helper()
		if got := tr.events(); !slices.Equal(got, want) {
			t.Errorf("events = %q, want %q", got, want)
		}
	}
}

func sessionWantContains(parts ...string) func(t *testing.T, tr sessionContractTrace) {
	return func(t *testing.T, tr sessionContractTrace) {
		t.Helper()
		text := sessionFrameText(tr.last())
		for _, part := range parts {
			if !strings.Contains(text, part) {
				t.Errorf("frame lacks %q:\n%s", part, text)
			}
		}
	}
}

func sessionWantLacks(parts ...string) func(t *testing.T, tr sessionContractTrace) {
	return func(t *testing.T, tr sessionContractTrace) {
		t.Helper()
		text := sessionFrameText(tr.last())
		for _, part := range parts {
			if strings.Contains(text, part) {
				t.Errorf("frame has %q:\n%s", part, text)
			}
		}
	}
}

func sessionWantRows(n int) func(t *testing.T, tr sessionContractTrace) {
	return func(t *testing.T, tr sessionContractTrace) {
		t.Helper()
		if got := len(sessionRows(tr.last())); got != n {
			t.Errorf("%d session rows drawn, want %d:\n%s", got, n, sessionFrameText(tr.last()))
		}
	}
}

func sessionWantScroll(want string) func(t *testing.T, tr sessionContractTrace) {
	return func(t *testing.T, tr sessionContractTrace) {
		t.Helper()
		if got := sessionScroll(tr.last()); got != want {
			t.Errorf("position line = %q, want %q", got, want)
		}
	}
}

const (
	skCtrlS   = "\x13"
	skCtrlN   = "\x0e"
	skCtrlP   = "\x10"
	skCtrlD   = "\x04"
	skCtrlR   = "\x12"
	skCtrlBS  = "\x1b[127;5u"
	skTab     = "\t"
	skUp      = "\x1b[A"
	skDown    = "\x1b[B"
	skPageUp  = "\x1b[5~"
	skPageDn  = "\x1b[6~"
	skEnter   = "\r"
	skEscape  = "\x1b"
	skCtrlG   = "\x07"
	sessionS3 = "/work/sessions/s03.jsonl"
)

// sessionListInputCases are one obligation each of SessionList.handleInput (session-selector.ts:530-636) in Pi's branch order. An empty query leaves the
// recent order as the loader returned it (s00 ... s13 for the big fixture), which makes the highlighted row predictable.
func sessionListInputCases() []sessionContractCase {
	recent := skCtrlS
	pages := func(n int) []string {
		keys := []string{recent}
		for range n {
			keys = append(keys, skPageDn)
		}
		return keys
	}
	return []sessionContractCase{
		// branch 1: delete confirmation swallows every key but confirm and cancel
		{name: "confirm-delete/confirm-key-deletes-and-reports-trash", fixture: "big", keys: []string{recent, skCtrlD, skEnter}, check: sessionWantAll(sessionWantContains("Session moved to trash"), sessionWantLacks("Delete session?"))},
		{name: "confirm-delete/cancel-key-keeps-the-session", fixture: "big", keys: []string{recent, skCtrlD, skEscape}, check: sessionWantAll(sessionWantLacks("Delete session?", "Session moved to trash"), sessionWantEvents())},
		{name: "confirm-delete/cancel-does-not-cancel-the-selector", fixture: "big", keys: []string{skCtrlD, skEscape, skEscape}, check: sessionWantEvents("cancel")},
		{name: "confirm-delete/other-keys-are-ignored", fixture: "big", keys: []string{recent, skCtrlD, skDown, skUp, skPageDn, "a", skTab, skCtrlS, skCtrlN, skCtrlP, skCtrlD, skCtrlR}, check: sessionWantAll(sessionWantContains("Delete session?", "Sort: Recent", "Name: All", "Resume Session (Current Folder)"), sessionWantSelected("message number 0 "), sessionWantLacks("path (on)"))},
		{name: "confirm-delete/typing-does-not-reach-the-search", fixture: "big", keys: []string{skCtrlD, "a", "b"}, check: sessionWantAll(sessionWantContains("Delete session?"), sessionWantRows(10))},
		{name: "confirm-delete/marks-the-pending-row-in-the-error-colour", fixture: "big", keys: []string{recent, skCtrlD}, check: func(t *testing.T, tr sessionContractTrace) {
			t.Helper()
			var row string
			for _, line := range tr.last() {
				if strings.Contains(sessionPlain(line), "message number 0 ") {
					row = line
				}
			}
			if !strings.Contains(row, tui.ActiveTheme().Error) {
				t.Errorf("pending row %q lacks the error colour %q", row, tui.ActiveTheme().Error)
			}
		}},
		{name: "confirm-delete/enter-does-not-select-the-session", fixture: "big", keys: []string{skCtrlD, skEnter}, check: sessionWantEvents()},
		{name: "confirm-delete/rebound-confirm-key", fixture: "big", bindings: map[string][]string{"tui.select.confirm": {"ctrl+g"}}, keys: []string{skCtrlD, skCtrlG}, check: sessionWantContains("Session moved to trash")},
		{name: "confirm-delete/rebound-cancel-key", fixture: "big", bindings: map[string][]string{"tui.select.cancel": {"ctrl+g"}}, keys: []string{skCtrlD, skCtrlG}, check: sessionWantLacks("Delete session?")},
		{name: "confirm-delete/old-cancel-key-is-ignored-after-rebinding", fixture: "big", bindings: map[string][]string{"tui.select.cancel": {"ctrl+g"}}, keys: []string{skCtrlD, skEscape}, check: sessionWantContains("Delete session?")},
		// branch 2: tui.input.tab
		{name: "tab/switches-to-the-all-scope", fixture: "big", keys: []string{skTab}, check: sessionWantAll(sessionWantContains("Resume Session (All)", "◉ All"), sessionWantSelected("message number 21"))},
		{name: "tab/switches-back-to-the-current-scope", fixture: "big", keys: []string{skTab, skTab}, check: sessionWantContains("Resume Session (Current Folder)", "◉ Current Folder")},
		{name: "tab/shows-the-working-directory-in-the-all-scope", fixture: "big", keys: []string{skTab}, check: sessionWantContains("/other/place")},
		{name: "tab/is-not-typed-into-the-search", fixture: "big", keys: []string{skTab}, check: sessionWantContains("> ")},
		{name: "tab/empty-current-scope-offers-the-all-scope", fixture: "empty-current", keys: []string{}, check: sessionWantContains("No sessions in current folder. Press Tab to view all.")},
		{name: "tab/lists-the-all-scope-after-an-empty-current-scope", fixture: "empty-current", keys: []string{skTab}, check: sessionWantRows(10)},
		{name: "tab/empty-everywhere-says-so", fixture: "empty", keys: []string{skTab}, check: sessionWantContains("No sessions found")},
		{name: "tab/untouched-selection-restarts-at-the-first-row", fixture: "big", keys: []string{skTab}, check: sessionWantSelected("message number 21")},
		{name: "tab/touched-selection-is-lost-while-the-other-scope-is-still-loading", fixture: "big", keys: []string{recent, skDown, skDown, skTab}, check: sessionWantSelected("message number 0 ")},
		{name: "tab/touched-selection-follows-its-session-into-a-loaded-scope", fixture: "big", keys: []string{skTab, skDown, skDown, skDown, skTab}, check: func(t *testing.T, tr sessionContractTrace) {
			t.Helper()
			before, after := sessionMessage.FindString(sessionSelected(tr.Frames[len(tr.Frames)-2])), sessionMessage.FindString(sessionSelected(tr.last()))
			if before == "" || before != after {
				t.Errorf("highlighted session %q before the switch and %q after", before, after)
			}
		}},
		{name: "tab/untouched-selection-of-a-loaded-scope-restarts-at-the-first-row", fixture: "big", keys: []string{skTab, skTab}, check: sessionWantSelected("named-13")},
		{name: "tab/search-survives-the-scope-switch", fixture: "big", keys: []string{"t", "o", "p", "i", "c", "1", skTab}, check: sessionWantAll(sessionWantContains("topic1"), sessionWantRows(5))},
		{name: "tab/rebound-key", fixture: "big", bindings: map[string][]string{"tui.input.tab": {"ctrl+g"}}, keys: []string{skCtrlG}, check: sessionWantContains("Resume Session (All)")},
		{name: "tab/old-key-after-rebinding-does-nothing", fixture: "big", bindings: map[string][]string{"tui.input.tab": {"ctrl+g"}}, keys: []string{skTab}, check: sessionWantContains("Resume Session (Current Folder)")},
		// branch 3: app.session.toggleSort
		{name: "sort/threaded-to-recent", fixture: "big", keys: []string{skCtrlS}, check: sessionWantAll(sessionWantContains("Sort: Recent"), sessionWantSelected("message number 0 "))},
		{name: "sort/recent-to-fuzzy", fixture: "big", keys: []string{skCtrlS, skCtrlS}, check: sessionWantContains("Sort: Fuzzy")},
		{name: "sort/fuzzy-to-threaded", fixture: "big", keys: []string{skCtrlS, skCtrlS, skCtrlS}, check: sessionWantContains("Sort: Threaded")},
		{name: "sort/threaded-draws-the-tree", fixture: "big", keys: []string{}, check: sessionWantContains("└─")},
		{name: "sort/recent-draws-a-flat-list", fixture: "big", keys: []string{skCtrlS}, check: sessionWantLacks("└─")},
		{name: "sort/keeps-the-selected-index", fixture: "big", keys: []string{skDown, skDown, skDown, skCtrlS}, check: sessionWantSelected("message number 3 ")},
		{name: "sort/is-not-typed-into-the-search", fixture: "big", keys: []string{skCtrlS}, check: sessionWantRows(10)},
		{name: "sort/fuzzy-orders-by-relevance", fixture: "big", keys: []string{skCtrlS, skCtrlS, "t", "o", "p", "i", "c", "2"}, check: sessionWantRows(4)},
		// branch 4: app.session.toggleNamedFilter (the component's own manager)
		{name: "named/filters-to-named-sessions", fixture: "big", keys: []string{skCtrlN}, check: sessionWantAll(sessionWantContains("Name: Named"), sessionWantRows(4))},
		{name: "named/toggles-back-to-all", fixture: "big", keys: []string{skCtrlN, skCtrlN}, check: sessionWantAll(sessionWantContains("Name: All"), sessionWantRows(10))},
		{name: "named/names-padded-by-javascript-whitespace-count", fixture: "big", keys: []string{skCtrlN}, check: sessionWantContains("spaced")},
		{name: "named/empty-current-scope-message", fixture: "unnamed", keys: []string{skCtrlN}, check: sessionWantContains("No named sessions in current folder. Press ctrl+n to show all, or Tab to view all.")},
		{name: "named/empty-all-scope-message", fixture: "unnamed", keys: []string{skTab, skCtrlN}, check: sessionWantContains("No named sessions found. Press ctrl+n to show all.")},
		{name: "named/with-a-search", fixture: "big", keys: []string{"n", "a", "m", "e", "d", "-", "1", skCtrlN}, check: sessionWantRows(2)},
		{name: "named/clamps-the-selection", fixture: "big", keys: []string{skDown, skDown, skDown, skDown, skDown, skCtrlN}, check: sessionWantSelected("named")},
		{name: "named/is-not-typed-into-the-search", fixture: "big", keys: []string{skCtrlN}, check: sessionWantContains("> ")},
		// branch 5: app.session.togglePath
		{name: "path/shows-the-session-path", fixture: "big", keys: []string{skCtrlP}, check: sessionWantAll(sessionWantContains("path (on)"), sessionWantSelected("/work/sessions/"))},
		{name: "path/hides-it-again", fixture: "big", keys: []string{skCtrlP, skCtrlP}, check: sessionWantAll(sessionWantContains("path (off)"), sessionWantLacks("/work/sessions/"))},
		{name: "path/survives-the-scope-switch", fixture: "big", keys: []string{skCtrlP, skTab}, check: sessionWantContains("path (on)", "/work/sessions/")},
		{name: "path/is-not-typed-into-the-search", fixture: "big", keys: []string{skCtrlP}, check: sessionWantRows(10)},
		// branch 6: app.session.delete
		{name: "delete/asks-for-confirmation", fixture: "big", keys: []string{recent, skCtrlD}, check: sessionWantContains("Delete session?", "enter confirm", "escape/ctrl+c cancel")},
		{name: "delete/refuses-the-active-session", fixture: "big", currentPath: sessionS3, keys: []string{recent, skDown, skDown, skDown, skCtrlD}, check: sessionWantAll(sessionWantContains("Cannot delete the currently active session"), sessionWantLacks("Delete session?"))},
		{name: "delete/other-sessions-are-not-refused", fixture: "big", currentPath: sessionS3, keys: []string{recent, skCtrlD}, check: sessionWantAll(sessionWantContains("Delete session?"), sessionWantLacks("Cannot delete"))},
		{name: "delete/active-session-row-is-accent-coloured", fixture: "big", currentPath: sessionS3, keys: []string{recent, skDown, skDown, skDown}, check: func(t *testing.T, tr sessionContractTrace) {
			t.Helper()
			for _, line := range tr.last() {
				if strings.Contains(sessionPlain(line), "message number 3 ") && !strings.Contains(line, tui.ActiveTheme().Accent) {
					t.Errorf("active session row %q lacks the accent colour", line)
				}
			}
		}},
		{name: "delete/empty-list-does-nothing", fixture: "empty", keys: []string{skCtrlD}, check: sessionWantLacks("Delete session?", "Cannot delete")},
		{name: "delete/after-the-cursor-leaves-an-empty-list", fixture: "empty", keys: []string{skDown, skCtrlD}, check: sessionWantLacks("Delete session?")},
		{name: "delete/confirmation-follows-the-highlighted-row", fixture: "big", keys: []string{recent, skDown, skCtrlD}, check: sessionWantContains("Delete session?")},
		// branch 7: app.session.rename
		{name: "rename/opens-the-panel", fixture: "big", keys: []string{skCtrlR}, check: sessionWantContains("Rename Session", "enter to save · escape/ctrl+c to cancel")},
		{name: "rename/without-a-rename-function-does-nothing", fixture: "big", noRename: true, keys: []string{skCtrlR}, check: sessionWantAll(sessionWantLacks("Rename Session"), sessionWantContains("Resume Session"))},
		{name: "rename/empty-list-does-nothing", fixture: "empty", keys: []string{skCtrlR}, check: sessionWantLacks("Rename Session")},
		{name: "rename/hint-appears-only-with-a-rename-function", fixture: "big", noRename: true, keys: []string{}, check: sessionWantLacks("rename")},
		{name: "rename/prefills-the-name-of-the-highlighted-session", fixture: "big", keys: []string{recent, skPageDn, skCtrlR}, check: sessionWantAll(sessionWantContains("Rename Session"), sessionWantLacks("Resume Session"))},
		{name: "rename/rebound-key", fixture: "big", bindings: map[string][]string{"app.session.rename": {"ctrl+g"}}, keys: []string{skCtrlG}, check: sessionWantContains("Rename Session")},
		// branch 8: app.session.deleteNoninvasive
		{name: "ctrl-backspace/empty-query-asks-for-delete-confirmation", fixture: "big", keys: []string{recent, skCtrlBS}, check: sessionWantContains("Delete session?")},
		{name: "ctrl-backspace/with-a-query-is-forwarded-to-the-search-not-a-delete", fixture: "big", keys: []string{"t", "o", "p", "i", "c", "1", skCtrlBS}, check: sessionWantAll(sessionWantLacks("Delete session?"), sessionWantRows(5))},
		{name: "ctrl-backspace/deletes-a-word-and-refilters", fixture: "big", keys: []string{"t", "o", "p", "i", "c", "1", " ", "d", "e", "p", "l", skCtrlBS}, check: sessionWantAll(sessionWantLacks("Delete session?"), sessionWantContains("topic1"))},
		{name: "ctrl-backspace/refuses-the-active-session", fixture: "big", currentPath: sessionS3, keys: []string{recent, skDown, skDown, skDown, skCtrlBS}, check: sessionWantContains("Cannot delete the currently active session")},
		{name: "ctrl-backspace/empty-list-does-nothing", fixture: "empty", keys: []string{skCtrlBS}, check: sessionWantLacks("Delete session?")},
		{name: "ctrl-backspace/query-emptied-then-asks", fixture: "big", keys: []string{"a", "\x7f", skCtrlBS}, check: sessionWantContains("Delete session?")},
		// branches 9-14: list actions after the touch flag
		{name: "up/clamps-at-the-first-row-without-wrapping", fixture: "big", keys: []string{recent, skUp, skUp}, check: sessionWantSelected("message number 0 ")},
		{name: "up/moves-one-row", fixture: "big", keys: []string{recent, skDown, skDown, skUp}, check: sessionWantSelected("named-1")},
		{name: "down/moves-one-row", fixture: "big", keys: []string{recent, skDown}, check: sessionWantSelected("named-1")},
		{name: "down/clamps-at-the-last-row-without-wrapping", fixture: "big", keys: append(pages(2), skDown, skDown, skDown, skDown, skDown), check: sessionWantAll(sessionWantSelected("named-13"), sessionWantScroll("(14/14)"))},
		{name: "down/single-session-stays", fixture: "one", keys: []string{skDown, skDown}, check: sessionWantSelected("message number 0")},
		{name: "up/single-session-stays", fixture: "one", keys: []string{skUp}, check: sessionWantSelected("message number 0")},
		{name: "down/empty-list-selects-nothing", fixture: "empty", keys: []string{skDown, skDown, skEnter}, check: sessionWantEvents()},
		{name: "up/after-down-on-an-empty-list-returns-to-the-first-index", fixture: "empty", keys: []string{skDown, skUp, skEnter}, check: sessionWantEvents()},
		{name: "page-up/moves-ten-rows", fixture: "big", keys: append(pages(1), skPageUp), check: sessionWantSelected("message number 0 ")},
		{name: "page-up/clamps-at-the-first-row", fixture: "big", keys: []string{recent, skDown, skDown, skPageUp}, check: sessionWantSelected("message number 0 ")},
		{name: "page-up/single-session-stays", fixture: "one", keys: []string{skPageUp}, check: sessionWantSelected("message number 0")},
		{name: "page-down/moves-ten-rows", fixture: "big", keys: pages(1), check: sessionWantAll(sessionWantSelected("message number 10 "), sessionWantScroll("(11/14)"))},
		{name: "page-down/clamps-at-the-last-row", fixture: "big", keys: pages(3), check: sessionWantAll(sessionWantSelected("named-13"), sessionWantScroll("(14/14)"))},
		{name: "page-down/single-session-stays", fixture: "one", keys: []string{skPageDn}, check: sessionWantSelected("message number 0")},
		{name: "page-down/empty-list-stays-empty", fixture: "empty", keys: []string{skPageDn, skPageUp}, check: sessionWantContains("No sessions in current folder")},
		{name: "confirm/selects-the-highlighted-session", fixture: "big", keys: []string{recent, skEnter}, check: sessionWantEvents("select:/work/sessions/s00.jsonl")},
		{name: "confirm/selects-after-a-move", fixture: "big", keys: []string{recent, skDown, skDown, skEnter}, check: sessionWantEvents("select:/work/sessions/s02.jsonl")},
		{name: "confirm/empty-list-selects-nothing", fixture: "empty", keys: []string{skEnter}, check: sessionWantEvents()},
		{name: "confirm/search-input-submit-selects-when-confirm-is-rebound", fixture: "big", bindings: map[string][]string{"tui.select.confirm": {"ctrl+g"}}, keys: []string{recent, skEnter}, check: sessionWantEvents("select:/work/sessions/s00.jsonl")},
		{name: "confirm/rebound-key-selects", fixture: "big", bindings: map[string][]string{"tui.select.confirm": {"ctrl+g"}}, keys: []string{recent, skCtrlG}, check: sessionWantEvents("select:/work/sessions/s00.jsonl")},
		{name: "cancel/reports-cancel", fixture: "big", keys: []string{skEscape}, check: sessionWantEvents("cancel")},
		{name: "cancel/does-not-clear-the-search-first", fixture: "big", keys: []string{"a", skEscape}, check: sessionWantEvents("cancel")},
		{name: "cancel/empty-list", fixture: "empty", keys: []string{skEscape}, check: sessionWantEvents("cancel")},
		{name: "cancel/ctrl-c-is-the-second-default-key", fixture: "big", keys: []string{"\x03"}, check: sessionWantEvents("cancel")},
		{name: "cancel/rebound-key", fixture: "big", bindings: map[string][]string{"tui.select.cancel": {"ctrl+g"}}, keys: []string{skCtrlG}, check: sessionWantEvents("cancel")},
		// branch 15: everything else edits the search
		{name: "search/typing-filters-the-list", fixture: "big", keys: []string{"t", "o", "p", "i", "c", "1"}, check: sessionWantAll(sessionWantRows(5), sessionWantContains("topic1"))},
		{name: "search/backspace-widens-the-list", fixture: "big", keys: []string{"t", "o", "p", "i", "c", "1", "\x7f"}, check: sessionWantRows(10)},
		{name: "search/no-match-shows-the-empty-list", fixture: "big", keys: []string{"q", "q", "q", "q"}, check: sessionWantContains("No sessions in current folder. Press Tab to view all.")},
		{name: "search/clamps-the-selection-to-the-shorter-list", fixture: "big", keys: []string{recent, skPageDn, "t", "o", "p", "i", "c", "1"}, check: sessionWantSelected("named-13")},
		{name: "search/regex-query", fixture: "big", keys: []string{"r", "e", ":", "n", "u", "m", "b", "e", "r", " ", "1", "[0-3]"}, check: sessionWantContains("re:number 1[0-3]")},
		{name: "search/phrase-query", fixture: "big", keys: []string{"\"", "n", "u", "m", "b", "e", "r", " ", "7", "\""}, check: sessionWantRows(1)},
		{name: "search/threaded-sort-with-a-query-is-flat", fixture: "big", keys: []string{"t", "o", "p", "i", "c", "1"}, check: sessionWantLacks("└─", "├─")},
		{name: "search/whitespace-only-query-keeps-the-tree", fixture: "big", keys: []string{" ", " "}, check: sessionWantContains("└─")},
		{name: "search/cursor-keys-edit-the-input", fixture: "big", keys: []string{"a", "b", "\x1b[D", "x"}, check: sessionWantContains("axb")},
		{name: "search/paste", fixture: "big", keys: []string{"\x1b[200~topic2\x1b[201~"}, check: sessionWantContains("topic2")},
		{name: "search/unbound-control-keys-edit-the-input", fixture: "big", keys: []string{"a", "b", "\x01", "x"}, check: sessionWantContains("xab")},
		{name: "search/empty-chunk-changes-nothing", fixture: "big", keys: []string{""}, check: sessionWantRows(10)},
		{name: "search/typing-does-not-touch-the-selection-flag-semantics", fixture: "big", keys: []string{"a", skTab}, check: sessionWantContains("Resume Session (All)")},
	}
}

func TestSessionListHandleInputContractsMatchPi(t *testing.T) {
	runSessionContracts(t, sessionListInputCases())
}

// sessionComponentInputCases are the obligations of SessionSelectorComponent.handleInput (session-selector.ts:689-701): rename mode sends the cancel
// key to exitRenameMode and every other key to the rename Input, list mode sends every key to the session list.
func sessionComponentInputCases() []sessionContractCase {
	recent := skCtrlS
	return []sessionContractCase{
		{name: "rename-mode/escape-leaves-rename-mode", fixture: "big", keys: []string{skCtrlR, skEscape}, check: sessionWantAll(sessionWantContains("Resume Session"), sessionWantLacks("Rename Session"), sessionWantEvents())},
		{name: "rename-mode/escape-does-not-cancel-the-selector", fixture: "big", keys: []string{skCtrlR, skEscape}, check: sessionWantEvents()},
		{name: "rename-mode/second-escape-cancels", fixture: "big", keys: []string{skCtrlR, skEscape, skEscape}, check: sessionWantEvents("cancel")},
		{name: "rename-mode/ctrl-c-is-the-second-default-cancel-key", fixture: "big", keys: []string{skCtrlR, "\x03"}, check: sessionWantLacks("Rename Session")},
		{name: "rename-mode/rebound-cancel-key-leaves", fixture: "big", bindings: map[string][]string{"tui.select.cancel": {"ctrl+g"}}, keys: []string{skCtrlR, skCtrlG}, check: sessionWantLacks("Rename Session")},
		{name: "rename-mode/old-cancel-key-is-inert-after-rebinding", fixture: "big", bindings: map[string][]string{"tui.select.cancel": {"ctrl+g"}}, keys: []string{skCtrlR, skEscape}, check: sessionWantContains("Rename Session")},
		{name: "rename-mode/typing-edits-the-rename-input", fixture: "big", keys: []string{recent, skCtrlR, "x", "y"}, check: sessionWantContains("Rename Session", "> xy")},
		{name: "rename-mode/typing-does-not-edit-the-search", fixture: "big", keys: []string{recent, skCtrlR, "x", "y", skEscape}, check: sessionWantAll(sessionWantLacks("> xy"), sessionWantRows(10))},
		{name: "rename-mode/prefills-the-current-name", fixture: "big", keys: []string{recent, skDown, skCtrlR}, check: sessionWantContains("named-1")},
		{name: "rename-mode/enter-renames-the-session", fixture: "big", keys: []string{recent, skCtrlR, "n", "e", "w", skEnter}, check: sessionWantAll(sessionWantEvents("rename:/work/sessions/s00.jsonl:new"), sessionWantLacks("Rename Session"))},
		{name: "rename-mode/the-new-name-is-trimmed", fixture: "big", keys: []string{recent, skCtrlR, " ", "x", " ", skEnter}, check: sessionWantEvents("rename:/work/sessions/s00.jsonl:x")},
		{name: "rename-mode/javascript-whitespace-is-trimmed", fixture: "big", keys: []string{recent, skCtrlR, "\x1b[200~\u00a0\ufeffz\u2003\x1b[201~", skEnter}, check: sessionWantEvents("rename:/work/sessions/s00.jsonl:z")},
		{name: "rename-mode/blank-name-keeps-the-panel-open", fixture: "big", keys: []string{recent, skCtrlR, skEnter}, check: sessionWantAll(sessionWantContains("Rename Session"), sessionWantEvents())},
		{name: "rename-mode/whitespace-only-name-keeps-the-panel-open", fixture: "big", keys: []string{recent, skCtrlR, " ", " ", skEnter}, check: sessionWantAll(sessionWantContains("Rename Session"), sessionWantEvents())},
		{name: "rename-mode/list-keys-are-not-routed-to-the-list", fixture: "big", keys: []string{skCtrlR, skDown, skTab, skCtrlS, skCtrlN, skCtrlP, skCtrlD, skPageDn}, check: sessionWantContains("Rename Session")},
		{name: "rename-mode/list-state-is-unchanged-after-leaving", fixture: "big", keys: []string{skCtrlR, skDown, skTab, skCtrlS, skCtrlN, skCtrlP, skEscape}, check: sessionWantContains("Resume Session (Current Folder)", "Sort: Threaded", "Name: All", "path (off)")},
		{name: "rename-mode/search-text-survives-the-round-trip", fixture: "big", keys: []string{"t", "o", "p", "i", "c", "1", skCtrlR, skEscape}, check: sessionWantAll(sessionWantContains("topic1"), sessionWantRows(5))},
		{name: "rename-mode/selection-survives-the-round-trip", fixture: "big", keys: []string{recent, skDown, skDown, skCtrlR, skEscape}, check: sessionWantSelected("message number 2 ")},
		{name: "rename-mode/rename-key-is-not-nested", fixture: "big", keys: []string{skCtrlR, skCtrlR}, check: sessionWantContains("Rename Session")},
		{name: "rename-mode/header-and-list-are-hidden", fixture: "big", keys: []string{skCtrlR}, check: sessionWantLacks("Resume Session", "tab scope", "message number")},
		{name: "rename-mode/panel-shows-the-save-and-cancel-keys", fixture: "big", bindings: map[string][]string{"tui.select.confirm": {"ctrl+y"}, "tui.select.cancel": {"ctrl+g"}}, keys: []string{skCtrlR}, check: sessionWantContains("ctrl+y to save · ctrl+g to cancel")},
		{name: "rename-mode/the-input-is-focused", fixture: "big", keys: []string{skCtrlR}, check: func(t *testing.T, tr sessionContractTrace) {
			t.Helper()
			if !strings.Contains(strings.Join(tr.last(), "\n"), "\x1b_pi:c\x07") {
				t.Errorf("rename input has no cursor marker:\n%s", strings.Join(tr.last(), "\n"))
			}
		}},
		{name: "rename-mode/refreshes-the-list-after-renaming", fixture: "big", keys: []string{recent, skCtrlR, "z", skEnter, skDown}, check: sessionWantSelected("named-1")},
		{name: "list-mode/every-key-goes-to-the-list", fixture: "big", keys: []string{recent, skDown}, check: sessionWantSelected("named-1")},
		{name: "list-mode/escape-reaches-the-list-and-cancels", fixture: "big", keys: []string{skEscape}, check: sessionWantEvents("cancel")},
		{name: "list-mode/rename-is-offered-only-with-a-rename-function", fixture: "big", noRename: true, keys: []string{skCtrlR}, check: sessionWantLacks("Rename Session")},
		// loads that are still running
		{name: "loading/shows-the-loading-header-without-progress", fixture: "big", pending: &sessionPending{ReleaseBefore: -1}, keys: []string{}, check: sessionWantContains("○ Current Folder | Loading ...")},
		{name: "loading/shows-the-progress-counts", fixture: "big", pending: &sessionPending{Progress: []sessionProgress{{Loaded: 3, Total: 14}}, ReleaseBefore: -1}, keys: []string{}, check: sessionWantContains("Loading 3/14")},
		{name: "loading/progress-with-partial-sessions-lists-them", fixture: "big", pending: &sessionPending{Progress: []sessionProgress{{Loaded: 4, Total: 14, Partial: 4}}, ReleaseBefore: -1}, keys: []string{}, check: sessionWantAll(sessionWantContains("Loading 4/14"), sessionWantRows(4))},
		{name: "loading/the-latest-progress-wins", fixture: "big", pending: &sessionPending{Progress: []sessionProgress{{Loaded: 2, Total: 14, Partial: 2}, {Loaded: 9, Total: 14, Partial: 9}}, ReleaseBefore: -1}, keys: []string{}, check: sessionWantAll(sessionWantContains("Loading 9/14"), sessionWantRows(9))},
		{name: "loading/finishing-clears-the-loading-header", fixture: "big", pending: &sessionPending{Progress: []sessionProgress{{Loaded: 4, Total: 14, Partial: 4}}, ReleaseBefore: 0}, keys: []string{recent}, check: sessionWantAll(sessionWantLacks("Loading"), sessionWantRows(10))},
		{name: "loading/rename-is-refused-while-the-scope-loads", fixture: "big", pending: &sessionPending{Progress: []sessionProgress{{Loaded: 4, Total: 14, Partial: 4}}, ReleaseBefore: -1}, keys: []string{skCtrlR}, check: sessionWantLacks("Rename Session")},
		{name: "loading/rename-works-once-the-load-finished", fixture: "big", pending: &sessionPending{Progress: []sessionProgress{{Loaded: 4, Total: 14, Partial: 4}}, ReleaseBefore: 1}, keys: []string{skDown, skCtrlR}, check: sessionWantContains("Rename Session")},
		{name: "loading/touched-selection-survives-progressive-snapshots", fixture: "big", pending: &sessionPending{Progress: []sessionProgress{{Loaded: 4, Total: 14, Partial: 4}}, ReleaseBefore: 3}, keys: []string{recent, skDown, skDown, skUp}, check: sessionWantSelected("named-1")},
		{name: "loading/untouched-selection-restarts-at-the-first-row-after-the-load", fixture: "big", pending: &sessionPending{Progress: []sessionProgress{{Loaded: 4, Total: 14, Partial: 4}}, ReleaseBefore: 1}, keys: []string{recent, recent}, check: sessionWantSelected("message number 0 ")},
		{name: "loading/tab-to-the-all-scope-while-current-loads", fixture: "big", pending: &sessionPending{Progress: []sessionProgress{{Loaded: 4, Total: 14, Partial: 4}}, ReleaseBefore: -1}, keys: []string{skTab}, check: sessionWantContains("Resume Session (All)", "◉ All")},
		{name: "loading/tab-back-to-a-loading-scope-shows-loading-again-with-its-partial-rows", fixture: "big", pending: &sessionPending{Progress: []sessionProgress{{Loaded: 4, Total: 14, Partial: 4}}, ReleaseBefore: -1}, keys: []string{skTab, skTab}, check: sessionWantAll(sessionWantContains("Loading ..."), sessionWantRows(4))},
		{name: "loading/delete-confirmation-works-during-the-load", fixture: "big", pending: &sessionPending{Progress: []sessionProgress{{Loaded: 4, Total: 14, Partial: 4}}, ReleaseBefore: -1}, keys: []string{skCtrlD}, check: sessionWantContains("Delete session?")},
		{name: "loading/typing-filters-the-partial-list", fixture: "big", pending: &sessionPending{Progress: []sessionProgress{{Loaded: 6, Total: 14, Partial: 6}}, ReleaseBefore: -1}, keys: []string{"t", "o", "p", "i", "c", "1"}, check: sessionWantRows(2)},
	}
}

func TestSessionSelectorComponentHandleInputContractsMatchPi(t *testing.T) {
	runSessionContracts(t, sessionComponentInputCases())
}

// TestSessionSelectorKeybindingSourcesMatchPi pins which keybinding manager Pi consults for each action: app.session.toggleNamedFilter comes from
// the manager passed to the component, every other action from the process-wide manager that the hints also read.
func TestSessionSelectorKeybindingSourcesMatchPi(t *testing.T) {
	type action struct {
		id, defaultKey string
		acted          func(t *testing.T, tr sessionContractTrace)
	}
	actions := []action{
		{id: "app.session.toggleSort", defaultKey: skCtrlS, acted: sessionWantContains("Sort: Recent")},
		{id: "app.session.togglePath", defaultKey: skCtrlP, acted: sessionWantContains("path (on)")},
		{id: "app.session.delete", defaultKey: skCtrlD, acted: sessionWantContains("Delete session?")},
		{id: "app.session.rename", defaultKey: skCtrlR, acted: sessionWantContains("Rename Session")},
		{id: "app.session.deleteNoninvasive", defaultKey: skCtrlBS, acted: sessionWantContains("Delete session?")},
		{id: "app.session.toggleNamedFilter", defaultKey: skCtrlN, acted: sessionWantContains("Name: Named")},
	}
	notActed := func(a action) func(t *testing.T, tr sessionContractTrace) {
		return func(t *testing.T, tr sessionContractTrace) {
			t.Helper()
			text := sessionFrameText(tr.last())
			for _, marker := range []string{"Sort: Recent", "path (on)", "Delete session?", "Rename Session", "Name: Named"} {
				if strings.Contains(text, marker) {
					t.Errorf("%s: %q appeared, so the key acted", a.id, marker)
				}
			}
		}
	}
	rebound := func(id string) map[string][]string { return map[string][]string{id: {"ctrl+g"}} }
	none := map[string][]string{}
	var cases []sessionContractCase
	for _, a := range actions {
		own := a.id == "app.session.toggleNamedFilter"
		// A rebinding of the process-wide manager only.
		global := sessionContractCase{name: "source/" + a.id + "/process-wide-rebinding-new-key", fixture: "big", global: rebound(a.id), bindings: none, keys: []string{skCtrlG}}
		globalOld := sessionContractCase{name: "source/" + a.id + "/process-wide-rebinding-old-key", fixture: "big", global: rebound(a.id), bindings: none, keys: []string{a.defaultKey}}
		// A rebinding of the component's manager only.
		option := sessionContractCase{name: "source/" + a.id + "/component-rebinding-new-key", fixture: "big", global: none, bindings: rebound(a.id), keys: []string{skCtrlG}}
		optionOld := sessionContractCase{name: "source/" + a.id + "/component-rebinding-old-key", fixture: "big", global: none, bindings: rebound(a.id), keys: []string{a.defaultKey}}
		if own {
			global.check, globalOld.check, option.check, optionOld.check = notActed(a), a.acted, a.acted, notActed(a)
		} else {
			global.check, globalOld.check, option.check, optionOld.check = a.acted, notActed(a), notActed(a), a.acted
		}
		cases = append(cases, global, globalOld, option, optionOld)
	}
	// The hint rows read the process-wide manager for every action they show.
	for _, a := range actions {
		if a.id == "app.session.deleteNoninvasive" {
			continue
		}
		cases = append(cases,
			sessionContractCase{name: "hints/" + a.id + "/follow-the-process-wide-manager", fixture: "big", global: rebound(a.id), bindings: none, check: sessionWantContains("ctrl+g")},
			sessionContractCase{name: "hints/" + a.id + "/ignore-the-component-manager", fixture: "big", global: none, bindings: rebound(a.id), check: sessionWantLacks("ctrl+g")})
	}
	cases = append(cases,
		sessionContractCase{name: "hints/empty-named-message-follows-the-process-wide-manager", fixture: "unnamed", global: rebound("app.session.toggleNamedFilter"), bindings: none, keys: []string{skCtrlN}, check: sessionWantContains("Press ctrl+g to show all")},
	)
	runSessionContracts(t, cases)
}

// TestSessionSelectorSharedKeysFollowRebinding rebinds each tui.* key the list reads to ctrl+g: the new key acts and the old one does not.
func TestSessionSelectorSharedKeysFollowRebinding(t *testing.T) {
	recent := skCtrlS
	type remap struct {
		id               string
		before           []string
		oldKey           string
		newCheck, oldChk func(t *testing.T, tr sessionContractTrace)
	}
	remaps := []remap{
		{"tui.input.tab", nil, skTab, sessionWantContains("Resume Session (All)"), sessionWantContains("Resume Session (Current Folder)")},
		{"tui.select.up", []string{recent, skDown, skDown}, skUp, sessionWantSelected("named-1"), sessionWantSelected("message number 2 ")},
		{"tui.select.down", []string{recent}, skDown, sessionWantSelected("named-1"), sessionWantSelected("message number 0 ")},
		{"tui.select.pageUp", []string{recent, skPageDn}, skPageUp, sessionWantSelected("message number 0 "), sessionWantSelected("message number 10 ")},
		{"tui.select.pageDown", []string{recent}, skPageDn, sessionWantSelected("message number 10 "), sessionWantSelected("message number 0 ")},
		// The old key still selects: the search input submits on Enter, so only the list action moves to the new key.
		{"tui.select.confirm", []string{recent}, skEnter, sessionWantEvents("select:/work/sessions/s00.jsonl"), sessionWantEvents("select:/work/sessions/s00.jsonl")},
		{"tui.select.cancel", nil, skEscape, sessionWantEvents("cancel"), sessionWantEvents()},
	}
	var cases []sessionContractCase
	for _, r := range remaps {
		b := map[string][]string{r.id: {"ctrl+g"}}
		cases = append(cases,
			sessionContractCase{name: "remap/" + r.id + "/new-key-acts", fixture: "big", bindings: b, keys: append(append([]string{}, r.before...), skCtrlG), check: r.newCheck},
			sessionContractCase{name: "remap/" + r.id + "/old-key-is-inert", fixture: "big", bindings: b, keys: append(append([]string{}, r.before...), r.oldKey), check: r.oldChk},
		)
	}
	runSessionContracts(t, cases)
}

// A confirmed delete reports its outcome in the header (session-selector.ts:836-860): trash and unlink successes in the accent colour with
// their own messages, a failure in the error colour with "Failed to delete: " and the error text ("Unknown error" when there is none). Pi's trash
// path cannot fail in the oracle, so the outcomes are driven through the injected delete operation.
func TestSessionSelectorDeleteOutcomeReportsInTheHeader(t *testing.T) {
	previousTheme, previousCaps, previousKeys := tui.ActiveTheme(), tui.GetCapabilities(), tui.GetTUIKeybindings()
	t.Cleanup(func() {
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme.Name)
		tui.SetTUIKeybindings(previousKeys)
	})
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	DefaultKeybindingsManager().syncToTUI()
	_, big, _ := sessionOracleFixtures()
	toSessions := func() ([]SessionInfo, error) {
		var out []SessionInfo
		for _, s := range big {
			modified, _ := time.Parse("2006-01-02T15:04:05.000Z", s.Modified)
			out = append(out, SessionInfo{Path: s.Path, ID: s.ID, CWD: s.CWD, Name: s.Name, Created: modified, Modified: modified, MessageCount: s.MessageCount, FirstMessage: s.FirstMessage, AllMessagesText: s.AllMessagesText})
		}
		return out, nil
	}
	th := tui.ActiveTheme()
	for _, tc := range []struct {
		name   string
		result sessionDeleteResult
		want   string
		color  string
	}{
		{"trash", sessionDeleteResult{ok: true, method: sessionDeleteTrash}, "Session moved to trash", th.Accent},
		{"unlink", sessionDeleteResult{ok: true, method: sessionDeleteUnlink}, "Session deleted", th.Accent},
		{"failure", sessionDeleteResult{error: "disk on fire"}, "Failed to delete: disk on fire", th.Error},
		{"failure without text", sessionDeleteResult{}, "Failed to delete: Unknown error", th.Error},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var deleted []string
			selector := newLoadedSessionSelector(toSessions, toSessions, nil, nil, "", DefaultKeybindingsManager())
			selector.deleteSession = func(path string) sessionDeleteResult { deleted = append(deleted, path); return tc.result }
			selector.HandleInput(skCtrlS)
			selector.HandleInput(skCtrlD)
			selector.HandleInput(skEnter)
			selector.drainLoadUpdates()
			if !slices.Equal(deleted, []string{"/work/sessions/s00.jsonl"}) {
				t.Fatalf("deleted %q, want the highlighted session once", deleted)
			}
			var status string
			for _, line := range selector.Render(100) {
				if strings.Contains(sessionPlain(line), tc.want) {
					status = line
				}
			}
			if status == "" || !strings.Contains(status, tc.color) {
				t.Errorf("header status %q lacks %q in colour %q:\n%s", status, tc.want, tc.color, strings.Join(selector.Render(100), "\n"))
			}
			if strings.Contains(sessionFrameText(selector.Render(100)), "Delete session?") {
				t.Error("the confirmation stays open after the outcome")
			}
		})
	}
}

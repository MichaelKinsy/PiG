package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type sessionOracleInfo struct {
	Path              string `json:"path"`
	ID                string `json:"id"`
	CWD               string `json:"cwd"`
	Name              string `json:"name,omitempty"`
	ParentSessionPath string `json:"parentSessionPath,omitempty"`
	Modified          string `json:"modified"`
	MessageCount      int    `json:"messageCount"`
	FirstMessage      string `json:"firstMessage"`
	AllMessagesText   string `json:"allMessagesText"`
}

type sessionOracleProbe struct {
	Theme       string              `json:"theme"`
	Width       int                 `json:"width"`
	Current     []sessionOracleInfo `json:"current"`
	All         []sessionOracleInfo `json:"all"`
	CurrentPath string              `json:"currentPath"`
	Rename      bool                `json:"rename"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
}

type sessionOracleResult struct {
	Steps  [][]string `json:"steps"`
	Frames [][]string `json:"frames"`
}

func sessionOracleFixtures() (small, big, all []sessionOracleInfo) {
	mk := func(i int, name, parent string) sessionOracleInfo {
		return sessionOracleInfo{
			Path: fmt.Sprintf("/work/sessions/s%02d.jsonl", i), ID: fmt.Sprintf("id%02d", i), CWD: "/work/proj",
			Name: name, ParentSessionPath: parent, Modified: time.Date(2018, 2, 1, 12, i, 0, 0, time.UTC).Format("2006-01-02T15:04:05.000Z"),
			MessageCount: i + 1, FirstMessage: fmt.Sprintf("message number %d about topic%d", i, i%3), AllMessagesText: fmt.Sprintf("message number %d about topic%d deploy", i, i%3),
		}
	}
	for i := range 3 {
		small = append(small, mk(i, []string{"", "named", ""}[i], ""))
	}
	for i := range 14 {
		name, parent := "", ""
		if i%4 == 1 {
			name = fmt.Sprintf("named-%d", i)
		}
		if i%3 == 2 {
			parent = fmt.Sprintf("/work/sessions/s%02d.jsonl", i-1)
		}
		big = append(big, mk(i, name, parent))
	}
	all = append(append([]sessionOracleInfo{}, big...), mk(20, "elsewhere", ""), mk(21, "", ""))
	all[len(all)-1].CWD = "/other/place"
	return small, big, all
}

// session-selector.ts handleInput against pinned Pi: delete confirmation swallows keys, tab/sort/named/path toggle, rename mode
// owns its keys, up/down/page/confirm/cancel drive the list, and every other key edits the search whose query refilters.
func TestSessionSelectorInputMatchesPi(t *testing.T) {
	small, big, all := sessionOracleFixtures()
	scripts := [][]string{
		{},
		{"\x1b[B", "\x1b[B", "\x1b[A", "\r"},
		{"\x1b[A", "\x1b[A"},
		{"\x1b[6~", "\x1b[6~", "\x1b[5~", "\x1b[B"},
		{"\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B"},
		{"\t", "\x1b[B", "\t", "\x1b[B"},
		{"\x13", "\x13", "\x13", "\x13"},
		{"\x0e", "\x0e", "\x1b[B"},
		{"\x10", "\x10"},
		{"t", "o", "p", "i", "c", "1", "\x1b[B", "\x7f", "\x7f"},
		{"x", "y", "z", "\x1b[B", "\r", "\x1b"},
		{"\x04", "\x1b[B", "\x1b"},
		{"\x04", "a", "\x1b[B", "\x1b[A", "\x1b"},
		{"\x1b[B", "\x04", "\x04", "\x1b"},
		{"\x1b[B", "\x1b[B", "\x04", "\x1b"},
		{"\x1b"},
		{"\x08", "a", "\x08"},
		{"\x1b[127;5u", "\x1b[127;5u", "\x1b"},
		{"\x12", "x", "\x1b"},
		{"\x1b[B", "\x12", "\x1b[3~", "\x1b[B", "\x1b"},
		{"\x1b[B", "\x12", "n", "e", "w", "\r"},
		{"\x12", "\r"},
		{"\x12", "\x1b", "\x1b[B", "\r"},
		{"\x13", "\x1b[B", "\x1b[B", "\x0e", "\r"},
		{"\t", "\x1b[6~", "\x1b[6~", "\x1b[6~", "\r"},
		{"\x1b[B", "t", "\x1b[B", "\x7f", "\x1b[A"},
	}
	bindings := []map[string][]string{
		nil,
		{"tui.select.cancel": {"ctrl+x"}},
		{"tui.select.down": {"ctrl+n"}, "app.session.toggleNamedFilter": {"ctrl+g"}},
		{"app.session.rename": {"ctrl+d"}},
		{"tui.select.pageDown": {"down"}},
		{"tui.select.confirm": {"escape"}},
		{"app.session.toggleSort": {"tab"}},
	}
	fixtures := []struct {
		name         string
		current, all []sessionOracleInfo
		currentPath  string
	}{
		{"small", small, all, ""},
		{"big", big, all, "/work/sessions/s03.jsonl"},
		{"empty", []sessionOracleInfo{}, []sessionOracleInfo{}, ""},
		{"empty-current", []sessionOracleInfo{}, all, ""},
	}
	var probes []sessionOracleProbe
	for _, theme := range []string{"dark", "light"} {
		for _, fx := range fixtures {
			for _, b := range bindings {
				for _, keys := range scripts {
					probes = append(probes, sessionOracleProbe{Theme: theme, Width: 100, Current: fx.current, All: fx.all, CurrentPath: fx.currentPath, Rename: len(keys)%2 == 0, Bindings: b, Keys: keys})
				}
			}
		}
	}
	// Narrow terminals clip the header, hints, search, rows and the position line.
	for _, width := range []int{20, 40, 72} {
		for _, fx := range fixtures {
			for _, keys := range scripts {
				probes = append(probes, sessionOracleProbe{Theme: "dark", Width: width, Current: fx.current, All: fx.all, CurrentPath: fx.currentPath, Rename: len(keys)%2 == 0, Keys: keys})
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/session_selector.mjs", pigversion.UpstreamVersion)
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
	previousTheme, previousCaps, previousKeys := tui.ActiveTheme(), tui.GetCapabilities(), tui.GetTUIKeybindings()
	t.Cleanup(func() {
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme.Name)
		tui.SetTUIKeybindings(previousKeys)
	})
	toGo := func(infos []sessionOracleInfo) []SessionInfo {
		var out []SessionInfo
		for _, s := range infos {
			modified, _ := time.Parse("2006-01-02T15:04:05.000Z", s.Modified)
			out = append(out, SessionInfo{Path: s.Path, ID: s.ID, CWD: s.CWD, Name: s.Name, ParentSessionPath: s.ParentSessionPath, Created: modified, Modified: modified,
				MessageCount: s.MessageCount, FirstMessage: s.FirstMessage, AllMessagesText: s.AllMessagesText})
		}
		return out
	}
	failures := 0
	for i, probe := range probes {
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
		tui.SetTheme(probe.Theme)
		bindingMap := map[string][]KeyID{}
		for action, keys := range probe.Bindings {
			for _, key := range keys {
				bindingMap[action] = append(bindingMap[action], KeyID(key))
			}
		}
		km := DefaultKeybindingsManager()
		km.SetUserBindings(bindingMap)
		km.syncToTUI()
		var events []string
		var rename func(string, string) error
		if probe.Rename {
			rename = func(path, name string) error { events = append(events, "rename:"+path+":"+name); return nil }
		}
		current, allSessions := toGo(probe.Current), toGo(probe.All)
		selector := newLoadedSessionSelector(func() ([]SessionInfo, error) { return current, nil }, func() ([]SessionInfo, error) { return allSessions, nil }, rename, nil, probe.CurrentPath, km)
		selector.renameSession = rename
		selector.showRenameHint = rename != nil
		// Pi's deleteSessionFile reports a trash success for a path that does not exist.
		selector.deleteSession = func(string) sessionDeleteResult { return sessionDeleteResult{ok: true, method: sessionDeleteTrash} }
		got := sessionOracleResult{Steps: [][]string{}, Frames: [][]string{selector.Render(probe.Width)}}
		reported := 0
		// Pig reports completion through Done(), which the host polls before routing the next key; Pi calls onSelect/onCancel on
		// every later key, so a script is compared up to the key that completes the selector.
		completedAt := len(probe.Keys)
		for keyIndex, key := range probe.Keys {
			selector.HandleInput(key)
			selector.drainLoadUpdates()
			if selector.Done() && reported == 0 {
				if selector.Cancelled() {
					events = append(events, "cancel")
				} else {
					events = append(events, "select:"+selector.SelectedPath())
				}
				reported = 1
			}
			got.Steps = append(got.Steps, append([]string{}, events...))
			got.Frames = append(got.Frames, selector.Render(probe.Width))
			if selector.Done() {
				completedAt = keyIndex + 1
				break
			}
		}
		expected[i].Steps, expected[i].Frames = expected[i].Steps[:completedAt], expected[i].Frames[:completedAt+1]
		if !reflect.DeepEqual(got.Steps, expected[i].Steps) || !reflect.DeepEqual(got.Frames, expected[i].Frames) {
			failures++
			if failures <= 8 {
				t.Errorf("probe %d keys=%q bindings=%v rename=%v current=%d:\nsteps = %q\nPi    = %q\n%s", i, probe.Keys, probe.Bindings, probe.Rename, len(probe.Current), got.Steps, expected[i].Steps, firstFrameDifference(got.Frames, expected[i].Frames))
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

func firstFrameDifference(got, want [][]string) string {
	for f := range min(len(got), len(want)) {
		for l := range max(len(got[f]), len(want[f])) {
			var g, w string
			if l < len(got[f]) {
				g = got[f][l]
			}
			if l < len(want[f]) {
				w = want[f][l]
			}
			if g != w {
				return fmt.Sprintf("frame %d line %d:\n  pig %q\n  Pi  %q", f, l, g, w)
			}
		}
	}
	return "frames equal"
}

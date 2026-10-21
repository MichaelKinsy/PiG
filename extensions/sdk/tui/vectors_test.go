package tui

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// The vectors are what Pi's own functions return (test/parity/sdk-bulk/gen.mjs runs the pinned pi-tui sources); every SDK
// reproduces them, so a helper that returns a constant, or ports a different rule, fails here.
func loadVectors(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "test", "parity", "sdk-bulk", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatal(err)
	}
}

func TestTextVectorsMatchPi(t *testing.T) {
	var v struct {
		VisibleWidth [][2]any `json:"visibleWidth"`
		Strip        [][2]string
		Bg           [][2]string
		Truncate     [][5]any
		Wrap         [][3]any
		Slice        [][6]any
		Segments     [][6]any
		Link         [][3]any
		CursorMarker string
	}
	loadVectors(t, "tui-text.json", &v)
	if v.CursorMarker != CursorMarker {
		t.Fatalf("CursorMarker %q, Pi %q", CursorMarker, v.CursorMarker)
	}
	if len(v.VisibleWidth) < 1000 || len(v.Truncate) < 1000 || len(v.Wrap) < 1000 || len(v.Slice) < 1000 {
		t.Fatalf("vector file too small: %d/%d/%d/%d", len(v.VisibleWidth), len(v.Truncate), len(v.Wrap), len(v.Slice))
	}
	fails := 0
	check := func(ok bool, format string, args ...any) {
		if !ok {
			if fails++; fails <= 20 {
				t.Errorf(format, args...)
			}
		}
	}
	for _, c := range v.VisibleWidth {
		check(VisibleWidth(c[0].(string)) == int(c[1].(float64)), "VisibleWidth(%q) = %d, Pi %v", c[0], VisibleWidth(c[0].(string)), c[1])
	}
	for _, c := range v.Strip {
		check(StripTerminalSequences(c[0]) == c[1], "Strip(%q) = %q, Pi %q", c[0], StripTerminalSequences(c[0]), c[1])
	}
	for _, c := range v.Bg {
		check(GetActiveBackgroundAnsi(c[0]) == c[1], "Bg(%q) = %q, Pi %q", c[0], GetActiveBackgroundAnsi(c[0]), c[1])
	}
	for _, c := range v.Truncate {
		ellipsis, pad := DefaultEllipsis, false
		if c[2] != nil {
			ellipsis, pad = c[2].(string), c[3].(bool)
		}
		got := TruncateToWidth(c[0].(string), int(c[1].(float64)), ellipsis, pad)
		check(got == c[4], "Truncate%q = %q, Pi %q", c[:4], got, c[4])
	}
	for _, c := range v.Wrap {
		got := WrapTextWithAnsi(c[0].(string), int(c[1].(float64)))
		want := c[2].([]any)
		same := len(got) == len(want)
		for i := 0; same && i < len(got); i++ {
			same = got[i] == want[i]
		}
		check(same, "Wrap(%q, %v) = %q, Pi %q", c[0], c[1], got, want)
	}
	for _, c := range v.Slice {
		s, start, n, strict := c[0].(string), int(c[1].(float64)), int(c[2].(float64)), c[3].(bool)
		check(SliceByColumn(s, start, n, strict) == c[4], "SliceByColumn%q = %q, Pi %q", c[:4], SliceByColumn(s, start, n, strict), c[4])
		w := c[5].(map[string]any)
		got := SliceWithWidth(s, start, n, strict)
		check(got.Text == w["text"] && float64(got.Width) == w["width"].(float64), "SliceWithWidth%q = %+v, Pi %v", c[:4], got, w)
	}
	for _, c := range v.Segments {
		r := c[5].(map[string]any)
		got := ExtractSegments(c[0].(string), int(c[1].(float64)), int(c[2].(float64)), int(c[3].(float64)), c[4].(bool))
		check(got.Before == r["before"] && got.After == r["after"] && float64(got.BeforeWidth) == r["beforeWidth"].(float64) && float64(got.AfterWidth) == r["afterWidth"].(float64),
			"ExtractSegments%q = %+v, Pi %v", c[:5], got, r)
	}
	for _, c := range v.Link {
		got, ok := GetOsc8LinkAtColumn(c[0].(string), int(c[1].(float64)))
		check((c[2] == nil && !ok) || (c[2] != nil && ok && got == c[2]), "GetOsc8LinkAtColumn(%q, %v) = %q,%v, Pi %v", c[0], c[1], got, ok, c[2])
	}
}

func setKeyEnv(t *testing.T, env map[string]any) {
	for _, k := range []string{"WT_SESSION", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
	for k, val := range env {
		t.Setenv(k, val.(string))
	}
}

func TestKeyVectorsMatchPi(t *testing.T) {
	var v struct {
		Universe  []string
		Data      []string
		KeyHelper map[string]any
		Modes     []struct {
			Kitty bool
			Env   map[string]any
			Rows  [][6]any
		}
	}
	loadVectors(t, "tui-keys.json", &v)
	t.Cleanup(func() { SetKittyProtocolActive(false) })
	for name, want := range v.KeyHelper {
		if s, ok := want.(string); ok {
			if got := keyHelperValue(name); got != s {
				t.Errorf("Key.%s = %q, Pi %q", name, got, s)
			}
			continue
		}
		builders := map[string]func(string) string{
			"ctrl": Key.Ctrl, "shift": Key.Shift, "alt": Key.Alt, "super": Key.Super, "ctrlShift": Key.CtrlShift, "shiftCtrl": Key.ShiftCtrl,
			"ctrlAlt": Key.CtrlAlt, "altCtrl": Key.AltCtrl, "shiftAlt": Key.ShiftAlt, "altShift": Key.AltShift, "ctrlSuper": Key.CtrlSuper,
			"superCtrl": Key.SuperCtrl, "shiftSuper": Key.ShiftSuper, "superShift": Key.SuperShift, "altSuper": Key.AltSuper, "superAlt": Key.SuperAlt,
			"ctrlShiftAlt": Key.CtrlShiftAlt, "ctrlShiftSuper": Key.CtrlShiftSuper,
		}
		build, ok := builders[name]
		if !ok {
			t.Errorf("Key.%s is missing", name)
			continue
		}
		for i, k := range []string{"a", "Z", "enter", "f5", "[", "space"} {
			if got := build(k); got != want.([]any)[i] {
				t.Errorf("Key.%s(%q) = %q, Pi %v", name, k, got, want.([]any)[i])
			}
		}
	}
	fails := 0
	for _, mode := range v.Modes {
		setKeyEnv(t, mode.Env)
		SetKittyProtocolActive(mode.Kitty)
		for i, row := range mode.Rows {
			data := v.Data[i]
			fail := func(format string, args ...any) {
				if fails++; fails <= 20 {
					t.Errorf("kitty=%v env=%v %q: "+format, append([]any{mode.Kitty, mode.Env, data}, args...)...)
				}
			}
			parsed, ok := ParseKey(data)
			if (row[0] == nil) == ok || (ok && parsed != row[0]) {
				fail("ParseKey = %q,%v, Pi %v", parsed, ok, row[0])
			}
			if IsKeyRelease(data) != row[1].(bool) || IsKeyRepeat(data) != row[2].(bool) {
				fail("release/repeat = %v/%v, Pi %v/%v", IsKeyRelease(data), IsKeyRepeat(data), row[1], row[2])
			}
			printable, ok := DecodeKittyPrintable(data)
			if (row[3] == nil) == ok || (ok && printable != row[3]) {
				fail("DecodeKittyPrintable = %q,%v, Pi %v", printable, ok, row[3])
			}
			printable, ok = DecodePrintableKey(data)
			if (row[4] == nil) == ok || (ok && printable != row[4]) {
				fail("DecodePrintableKey = %q,%v, Pi %v", printable, ok, row[4])
			}
			matched := map[int]bool{}
			for _, idx := range row[5].([]any) {
				matched[int(idx.(float64))] = true
			}
			for j, id := range v.Universe {
				if got := MatchesKey(data, id); got != matched[j] {
					fail("MatchesKey(%q) = %v, Pi %v", id, got, matched[j])
				}
			}
		}
	}
}

func keyHelperValue(name string) string {
	h := map[string]string{
		"escape": Key.Escape, "esc": Key.Esc, "enter": Key.Enter, "return": Key.Return, "tab": Key.Tab, "space": Key.Space, "backspace": Key.Backspace,
		"delete": Key.Delete, "insert": Key.Insert, "clear": Key.Clear, "home": Key.Home, "end": Key.End, "pageUp": Key.PageUp, "pageDown": Key.PageDown,
		"up": Key.Up, "down": Key.Down, "left": Key.Left, "right": Key.Right, "f1": Key.F1, "f2": Key.F2, "f3": Key.F3, "f4": Key.F4, "f5": Key.F5, "f6": Key.F6, "f7": Key.F7, "f8": Key.F8, "f9": Key.F9, "f10": Key.F10, "f11": Key.F11, "f12": Key.F12, "backtick": Key.Backtick, "hyphen": Key.Hyphen,
		"equals": Key.Equals, "leftbracket": Key.Leftbracket, "rightbracket": Key.Rightbracket, "backslash": Key.Backslash, "semicolon": Key.Semicolon,
		"quote": Key.Quote, "comma": Key.Comma, "period": Key.Period, "slash": Key.Slash, "exclamation": Key.Exclamation, "at": Key.At, "hash": Key.Hash,
		"dollar": Key.Dollar, "percent": Key.Percent, "caret": Key.Caret, "ampersand": Key.Ampersand, "asterisk": Key.Asterisk, "leftparen": Key.Leftparen,
		"rightparen": Key.Rightparen, "underscore": Key.Underscore, "plus": Key.Plus, "pipe": Key.Pipe, "tilde": Key.Tilde, "leftbrace": Key.Leftbrace,
		"rightbrace": Key.Rightbrace, "colon": Key.Colon, "lessthan": Key.Lessthan, "greaterthan": Key.Greaterthan, "question": Key.Question,
	}
	return h[name]
}

func TestFuzzyVectorsMatchPi(t *testing.T) {
	var v struct {
		Match  [][4]any
		Filter [][3]any
	}
	loadVectors(t, "tui-fuzzy.json", &v)
	fails := 0
	for _, c := range v.Match {
		got := FuzzyMatch(c[0].(string), c[1].(string))
		if got.Matches != c[2].(bool) || (got.Matches && math.Float64bits(got.Score) != math.Float64bits(c[3].(float64)) && got.Score != c[3].(float64)) {
			if fails++; fails < 20 {
				t.Errorf("FuzzyMatch(%q, %q) = %+v, Pi %v %v", c[0], c[1], got, c[2], c[3])
			}
		}
	}
	for _, c := range v.Filter {
		var items []string
		for _, it := range c[0].([]any) {
			items = append(items, it.(string))
		}
		got := FuzzyFilter(items, c[1].(string), func(s string) string { return s })
		want := c[2].([]any)
		same := len(got) == len(want)
		for i := 0; same && i < len(got); i++ {
			same = got[i] == want[i]
		}
		if !same {
			if fails++; fails < 20 {
				t.Errorf("FuzzyFilter(%q, %q) = %q, Pi %q", items, c[1], got, want)
			}
		}
	}
}

func TestHyperlinkAndCapabilitiesMatchPi(t *testing.T) {
	var v struct {
		Capabilities []struct {
			Env      map[string]string
			Platform string
			Tmux     bool
			Detected struct {
				Images     *string
				TrueColor  bool
				Hyperlinks bool
			}
			TmuxProbed bool
		}
		Hyperlink    [][3]string
		CursorMarker string
	}
	loadVectors(t, "tui-terminal.json", &v)
	for _, c := range v.Hyperlink {
		if got := Hyperlink(c[0], c[1]); got != c[2] {
			t.Errorf("Hyperlink(%q, %q) = %q, Pi %q", c[0], c[1], got, c[2])
		}
	}
	probed := 0
	for i, c := range v.Capabilities {
		goos := "linux"
		if c.Platform == "win32" {
			goos = "windows"
		}
		probes := 0
		got := detect(func(k string) string { return c.Env[k] }, goos, func() bool { probes++; return c.Tmux })
		want := TerminalCapabilities{TrueColor: c.Detected.TrueColor, Hyperlinks: c.Detected.Hyperlinks}
		if c.Detected.Images != nil {
			want.Images = ImageProtocol(*c.Detected.Images)
		}
		if got != want || (probes > 0) != c.TmuxProbed {
			t.Errorf("case %d env=%v platform=%s tmux=%v: detect = %+v (probed %d), Pi %+v (probed %v)", i, c.Env, c.Platform, c.Tmux, got, probes, want, c.TmuxProbed)
		}
		if c.TmuxProbed {
			probed++
		}
	}
	if probed == 0 {
		t.Fatal("no vector exercises the tmux probe")
	}
}

func asStrings(t *testing.T, v any) []string {
	t.Helper()
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, item.(string))
	}
	return out
}

func asKeys(t *testing.T, v any) []string {
	t.Helper()
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		return asStrings(t, v)
	}
	return []string{}
}

func definitionsOf(t *testing.T, raw any) KeybindingDefinitions {
	t.Helper()
	defs := KeybindingDefinitions{}
	for id, def := range raw.(map[string]any) {
		m := def.(map[string]any)
		description, _ := m["description"].(string)
		defs[id] = KeybindingDefinition{DefaultKeys: asKeys(t, m["defaultKeys"]), Description: description}
	}
	return defs
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestKeybindingsVectorsMatchPi(t *testing.T) {
	var v struct {
		TuiKeybindings  map[string]any `json:"tuiKeybindings"`
		Merged          map[string]any
		Sample          []string
		DefaultResolved map[string]any
		Cases           []struct {
			Definitions string
			User        map[string]any
			Keys        map[string][]string
			Resolved    map[string]any
			Conflicts   []struct {
				Key         string
				Keybindings []string
			}
			UserBindings map[string]any
			Matches      map[string][]bool
			Definition   map[string]map[string]any
			AfterSet     struct {
				Keys      []string
				Resolved  map[string]any
				Conflicts []struct {
					Key         string
					Keybindings []string
				}
				UserBindings map[string]any
			}
		}
	}
	loadVectors(t, "tui-keybindings.json", &v)
	// Pi's TUI_KEYBINDINGS, and the default keys the SDK's global manager resolves, are Pi's own.
	for id, want := range definitionsOf(t, v.TuiKeybindings) {
		got, ok := TUIKeybindings[id]
		if !ok || !sameStrings(got.DefaultKeys, want.DefaultKeys) || got.Description != want.Description {
			t.Errorf("TUIKeybindings[%q] = %+v, Pi %+v", id, got, want)
		}
	}
	if len(TUIKeybindings) != len(v.TuiKeybindings) {
		t.Errorf("TUIKeybindings has %d entries, Pi %d", len(TUIKeybindings), len(v.TuiKeybindings))
	}
	for id, want := range v.DefaultResolved {
		if got := GetKeybindings().GetKeys(id); !sameStrings(got, asKeys(t, want)) {
			t.Errorf("GetKeybindings().GetKeys(%q) = %v, Pi %v", id, got, want)
		}
	}
	for n, c := range v.Cases {
		raw := v.TuiKeybindings
		if c.Definitions == "merged" {
			raw = v.Merged
		}
		user := KeybindingsConfig{}
		for id, keys := range c.User {
			user[id] = asKeys(t, keys)
		}
		m := NewKeybindingsManager(definitionsOf(t, raw), user)
		for id, want := range c.Keys {
			if got := m.GetKeys(id); !sameStrings(got, want) {
				t.Errorf("case %d GetKeys(%q) = %v, Pi %v", n, id, got, want)
			}
			def, ok := m.GetDefinition(id)
			if wantDef := c.Definition[id]; (wantDef == nil) == ok || (ok && !sameStrings(def.DefaultKeys, asKeys(t, wantDef["defaultKeys"]))) {
				t.Errorf("case %d GetDefinition(%q) = %+v,%v, Pi %v", n, id, def, ok, wantDef)
			}
			for i, data := range v.Sample {
				if got := m.Matches(data, id); got != c.Matches[id][i] {
					t.Errorf("case %d Matches(%q, %q) = %v, Pi %v", n, data, id, got, c.Matches[id][i])
				}
			}
		}
		for id, want := range c.Resolved {
			if got := m.GetResolvedBindings()[id]; !sameStrings(got, asKeys(t, want)) {
				t.Errorf("case %d GetResolvedBindings()[%q] = %v, Pi %v", n, id, got, want)
			}
		}
		if len(m.GetResolvedBindings()) != len(c.Resolved) {
			t.Errorf("case %d resolved has %d ids, Pi %d", n, len(m.GetResolvedBindings()), len(c.Resolved))
		}
		got := m.GetConflicts()
		if len(got) != len(c.Conflicts) {
			t.Errorf("case %d GetConflicts() = %v, Pi %v", n, got, c.Conflicts)
		}
		for i := range got {
			if i < len(c.Conflicts) && (got[i].Key != c.Conflicts[i].Key || !sameStrings(got[i].Keybindings, c.Conflicts[i].Keybindings)) {
				t.Errorf("case %d conflict %d = %+v, Pi %+v", n, i, got[i], c.Conflicts[i])
			}
		}
		for id, want := range c.UserBindings {
			if got := m.GetUserBindings()[id]; !sameStrings(got, asKeys(t, want)) {
				t.Errorf("case %d GetUserBindings()[%q] = %v, Pi %v", n, id, got, want)
			}
		}
		m.SetUserBindings(KeybindingsConfig{"tui.editor.cursorLeft": {"ctrl+g"}})
		if got := m.GetKeys("tui.editor.cursorLeft"); !sameStrings(got, c.AfterSet.Keys) {
			t.Errorf("case %d after SetUserBindings GetKeys = %v, Pi %v", n, got, c.AfterSet.Keys)
		}
		for id, want := range c.AfterSet.Resolved {
			if got := m.GetResolvedBindings()[id]; !sameStrings(got, asKeys(t, want)) {
				t.Errorf("case %d after SetUserBindings resolved[%q] = %v, Pi %v", n, id, got, want)
			}
		}
		if len(m.GetConflicts()) != len(c.AfterSet.Conflicts) {
			t.Errorf("case %d after SetUserBindings conflicts = %v, Pi %v", n, m.GetConflicts(), c.AfterSet.Conflicts)
		}
	}
}

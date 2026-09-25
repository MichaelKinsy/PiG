package codingagent

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// The keybindings page is a table of facts the registry already holds. A page
// written by hand drifts the moment a binding moves, and a wrong key in a
// reference table costs a reader more than a missing one: they conclude the
// feature is broken.
//
// This test lives in this package because the registry is unexported, and
// exporting it only so a test in another package could read it would add public
// surface for no production caller.

const keybindingsDocPath = "../../internal/pigdocs/content/keybindings.md"

var docRowRE = regexp.MustCompile("(?m)^\\| `(app\\.[a-zA-Z.]+)` \\| (.+?) \\|")

func documentedBindings(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(keybindingsDocPath))
	if err != nil {
		t.Fatalf("read the keybindings page: %v", err)
	}
	out := map[string]string{}
	for _, m := range docRowRE.FindAllStringSubmatch(string(data), -1) {
		action, keys := m[1], strings.TrimSpace(m[2])
		if _, ok := out[action]; ok {
			continue // later tables repeat an action for platform detail
		}
		out[action] = strings.Trim(keys, "`")
	}
	return out
}

func TestEveryKeybindingIsDocumented(t *testing.T) {
	documented := documentedBindings(t)
	for action := range appKeybindingDefinitionsFor(tui.KeybindingPlatformDarwin) {
		if _, ok := documented[action]; !ok {
			t.Errorf("%s has no row on the keybindings page, so a reader cannot discover it", action)
		}
	}
}

func TestNoDocumentedKeybindingHasBeenRemoved(t *testing.T) {
	defs := appKeybindingDefinitionsFor(tui.KeybindingPlatformDarwin)
	for action := range documentedBindings(t) {
		if _, ok := defs[action]; !ok {
			t.Errorf("the keybindings page documents %s, which no longer exists", action)
		}
	}
}

func TestDocumentedDefaultKeysMatchTheRegistry(t *testing.T) {
	documented := documentedBindings(t)
	for action, def := range appKeybindingDefinitionsFor(tui.KeybindingPlatformDarwin) {
		keys := make([]string, 0, len(def.DefaultKeys))
		for _, k := range def.DefaultKeys {
			keys = append(keys, string(k))
		}
		want := strings.Join(keys, ", ")
		if want == "" {
			want = "none"
		}
		if got := documented[action]; got != want {
			t.Errorf("%s: page says %q, registry says %q", action, got, want)
		}
	}
}

// A reader on Linux or Windows follows the platform table, so it must list every
// action whose default actually differs and no action whose default does not.
func TestThePlatformTableListsExactlyTheActionsThatDiffer(t *testing.T) {
	data, err := os.ReadFile(filepath.Clean(keybindingsDocPath))
	if err != nil {
		t.Fatalf("read the keybindings page: %v", err)
	}
	section := string(data)
	start := strings.Index(section, "## Platform differences")
	if start < 0 {
		t.Fatal("the keybindings page has no platform differences section")
	}
	end := strings.Index(section[start:], "\n## ")
	if end > 0 {
		section = section[start : start+end]
	} else {
		section = section[start:]
	}

	var listed []string
	for _, m := range docRowRE.FindAllStringSubmatch(section, -1) {
		listed = append(listed, m[1])
	}
	slices.Sort(listed)

	darwin := appKeybindingDefinitionsFor(tui.KeybindingPlatformDarwin)
	var differ []string
	for action := range darwin {
		for _, platform := range tui.KeybindingPlatforms {
			if !slices.Equal(darwin[action].DefaultKeys, appKeybindingDefinitionsFor(platform)[action].DefaultKeys) {
				differ = append(differ, action)
				break
			}
		}
	}
	slices.Sort(differ)

	if !slices.Equal(listed, differ) {
		t.Errorf("platform table lists %v, but these actions differ by platform: %v", listed, differ)
	}
}

// Each platform-table cell must match that platform's registry default.
func TestThePlatformTableCellsMatchTheRegistry(t *testing.T) {
	data, err := os.ReadFile(filepath.Clean(keybindingsDocPath))
	if err != nil {
		t.Fatalf("read the keybindings page: %v", err)
	}
	_, section, ok := strings.Cut(string(data), "## Platform differences")
	if !ok {
		t.Fatal("the keybindings page has no platform differences section")
	}
	if end := strings.Index(section[1:], "\n## "); end > 0 {
		section = section[:end+1]
	}
	columns := []tui.KeybindingPlatform{
		tui.KeybindingPlatformDarwin, tui.KeybindingPlatformLinux, tui.KeybindingPlatformLinuxWSL, tui.KeybindingPlatformWin32,
	}
	rowRE := regexp.MustCompile("(?m)^\\| `(app\\.[a-zA-Z.]+)` \\|(.*)\\|$")
	rows := rowRE.FindAllStringSubmatch(section, -1)
	if len(rows) == 0 {
		t.Fatal("the platform table has no rows")
	}
	for _, m := range rows {
		cells := strings.Split(m[2], "|")
		if len(cells) != len(columns) {
			t.Errorf("%s: %d platform cells, want %d", m[1], len(cells), len(columns))
			continue
		}
		for i, platform := range columns {
			want := strings.Join(appKeybindingDefinitionsFor(platform)[m[1]].DefaultKeys, ", ")
			if want == "" {
				want = "none"
			}
			if got := strings.Trim(strings.TrimSpace(cells[i]), "`"); got != want {
				t.Errorf("%s on %s: page says %q, registry says %q", m[1], platform, got, want)
			}
		}
	}
}

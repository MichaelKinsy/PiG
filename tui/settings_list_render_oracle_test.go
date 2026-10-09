package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type settingsListOracleItem struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

type settingsListOracleProbe struct {
	Items      []settingsListOracleItem `json:"items"`
	MaxVisible int                      `json:"maxVisible"`
	Search     bool                     `json:"search"`
	Keys       []string                 `json:"keys"`
	Widths     []int                    `json:"widths"`
}

// SettingsList.render and renderMainList (components/settings-list.ts) against pinned pi-tui: the label column capped at 36 cells, values truncated to the room left,
// the scroll counter, wrapped descriptions of the selected row, the empty and no-match messages and the hint line, with and without the search box, after
// arrow, page and typing keys, at widths from 1 to 120.
func TestSettingsListRenderMatchesPi(t *testing.T) {
	item := func(n int, label, value, description string) settingsListOracleItem {
		return settingsListOracleItem{ID: "id" + strconv.Itoa(n), Label: label, Value: value, Description: description}
	}
	sets := map[string][]settingsListOracleItem{
		"empty":  {},
		"small":  {item(1, "Theme", "dark", ""), item(2, "Editor padding", "2", "Columns of space either side of the editor text"), item(3, "Auto compact", "on", "")},
		"wide":   {item(1, "日本語の設定", "オン", "説明が長くて折り返される場合のテスト用の文章です"), item(2, "emoji 🙂 label", "🙂🙂", ""), item(3, "x", strings.Repeat("v", 90), "")},
		"long":   {item(1, strings.Repeat("L", 50), "value", "description of the long-label row that is long enough to wrap in narrow widths"), item(2, "short", "v", "")},
		"styled": {item(1, "\x1b[1mbold label\x1b[0m", "\x1b[32mgreen\x1b[0m", "styled \x1b[31mdescription\x1b[0m text"), item(2, "plain", "p", "")},
	}
	many := []settingsListOracleItem{}
	for i := range 14 {
		many = append(many, item(i, "Setting number "+strconv.Itoa(i), "value "+strconv.Itoa(i), ""))
	}
	many[3].Description = "The fourth setting has a description that wraps across more than one row at narrow widths."
	sets["many"] = many
	scripts := [][]string{{}, {"\x1b[B"}, {"\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B"}, {"\x1b[A"}, {"\x1b[A", "\x1b[A"}, {"\x1b[6~", "\x1b[6~"}, {"\x1b[5~"}, {"s", "e", "t"}, {"z", "z", "z"}, {"t", "\x7f", "\x7f"}, {"n", "\x1b[B", "\x1b[B", "\x1b[A"}, {"日", "x"}, {"\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B"}}
	var probes []settingsListOracleProbe
	for _, name := range []string{"empty", "small", "wide", "long", "styled", "many"} {
		for _, maxVisible := range []int{1, 3, 5, 10} {
			for _, search := range []bool{false, true} {
				for _, keys := range scripts {
					if len(keys) > 0 && !search && (keys[0] == "s" || keys[0] == "z" || keys[0] == "t" || keys[0] == "n" || keys[0] == "日") {
						continue
					}
					probes = append(probes, settingsListOracleProbe{Items: sets[name], MaxVisible: maxVisible, Search: search, Keys: keys, Widths: []int{1, 5, 12, 24, 50, 120}})
				}
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/settings_list_render.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][][][]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousKeybindings, previousCaps, previousKitty := GetKeybindings(), GetCapabilities(), IsKittyProtocolActive()
	SetKittyProtocolActive(false)
	t.Cleanup(func() {
		SetKeybindings(previousKeybindings)
		SetCapabilities(previousCaps)
		SetKittyProtocolActive(previousKitty)
	})
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetKeybindings(NewTUIKeybindingsManager(nil))
	sgr := func(open, closing int) func(string) string {
		return func(text string) string {
			return "\x1b[" + strconv.Itoa(open) + "m" + text + "\x1b[" + strconv.Itoa(closing) + "m"
		}
	}
	theme := SettingsListTheme{
		Label: func(text string, selected bool) string {
			if selected {
				return sgr(1, 22)(text)
			}
			return sgr(34, 39)(text)
		},
		Value: func(text string, selected bool) string {
			if selected {
				return sgr(7, 27)(text)
			}
			return sgr(32, 39)(text)
		},
		Description: sgr(2, 22),
		Cursor:      "> ",
		Hint:        sgr(3, 23),
	}
	failures := 0
	for i, probe := range probes {
		items := make([]SettingItem, len(probe.Items))
		for j, it := range probe.Items {
			items[j] = SettingItem{ID: it.ID, Label: it.Label, CurrentValue: it.Value, Description: it.Description}
		}
		list := NewSettingsList(items, probe.MaxVisible, theme, func(string, string) {}, func() {}, SettingsListOptions{EnableSearch: probe.Search})
		check := func(frame int) {
			for w, width := range probe.Widths {
				got := list.Render(width)
				want := slices.Clone(expected[i][frame][w])
				for r, row := range want {
					if widthx.VisibleWidth(row) > width { // D66: Pig clips a row Pi emits wider than the width
						want[r] = widthx.TruncateToWidth(row, width, "", false)
					}
				}
				if !reflect.DeepEqual(nonNil(got), nonNil(want)) {
					if failures++; failures <= 5 {
						spec, _ := json.Marshal(probe)
						t.Errorf("frame %d width %d %s\n  Pig %q\n  Pi  %q", frame, width, spec, got, want)
					}
				}
			}
		}
		check(0)
		for k, key := range probe.Keys {
			list.HandleInput(key)
			check(k + 1)
		}
	}
	if failures > 5 {
		t.Errorf("%d renders differ from Pi", failures)
	}
}

// getSettingsListTheme (theme.ts) against pinned Pi in both themes, byte for byte: the label and value for selected and unselected rows, the description and hint
// in the dim token, and the accent cursor, each closed with SGR 39.
func TestSettingsListThemeMatchesPi(t *testing.T) {
	texts := []string{"", "Theme", "dark", "日本語 🙂", "\x1b[1mbold\x1b[0m text"}
	type probe struct {
		Theme string   `json:"theme"`
		Texts []string `json:"texts"`
	}
	probes := []probe{{"dark", texts}, {"light", texts}}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/settings_list_theme.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	type row struct {
		Label       [2]string `json:"label"`
		Value       [2]string `json:"value"`
		Description string    `json:"description"`
		Hint        string    `json:"hint"`
	}
	var expected []struct {
		Cursor string `json:"cursor"`
		Rows   []row  `json:"rows"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps, previousTheme := GetCapabilities(), ActiveTheme()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	for i, p := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(p.Theme)
		theme := GetSettingsListTheme()
		if theme.Cursor != expected[i].Cursor {
			t.Errorf("%s cursor: Pig %q, Pi %q", p.Theme, theme.Cursor, expected[i].Cursor)
		}
		for j, text := range p.Texts {
			got := row{Label: [2]string{theme.Label(text, false), theme.Label(text, true)}, Value: [2]string{theme.Value(text, false), theme.Value(text, true)}, Description: theme.Description(text), Hint: theme.Hint(text)}
			if got != expected[i].Rows[j] {
				t.Errorf("%s %q:\n  Pig %q\n  Pi  %q", p.Theme, text, got, expected[i].Rows[j])
			}
		}
	}
}

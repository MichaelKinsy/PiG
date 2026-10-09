package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type acCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type acOp struct {
	Keys  *string `json:"keys,omitempty"`
	Max   *int    `json:"max,omitempty"`
	Width int     `json:"width"`
}

type acProbe struct {
	Theme    string      `json:"theme"`
	PaddingX int         `json:"paddingX"`
	MaxVis   *int        `json:"maxVisible"`
	Commands []acCommand `json:"commands"`
	Ops      []acOp      `json:"ops"`
}

type acFrame struct {
	Text string   `json:"text"`
	Rows []string `json:"rows"`
}

// acProvider is the slash-command provider the oracle script defines for Pi: a buffer that is "/" and non-space characters offers the commands
// whose names start with them; accepting replaces the prefix with the command and a space.
type acProvider struct{ commands []acCommand }

func (p acProvider) GetSuggestions(_ context.Context, lines []string, cursorLine, cursorCol int, _ AutocompleteSuggestionOptions) *AutocompleteSuggestions {
	before := lines[cursorLine][:cursorCol]
	if !strings.HasPrefix(before, "/") || strings.ContainsAny(before, " \t\n\r") {
		return nil
	}
	var items []AutocompleteItem
	for _, c := range p.commands {
		if strings.HasPrefix(c.Name, before[1:]) {
			items = append(items, AutocompleteItem{Value: c.Name, Label: c.Name, Description: c.Description})
		}
	}
	if len(items) == 0 {
		return nil
	}
	return &AutocompleteSuggestions{Items: items, Prefix: before}
}

func (p acProvider) ApplyCompletion(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) ([]string, int, int) {
	line := lines[cursorLine]
	start := max(0, cursorCol-len(prefix))
	out := append([]string{}, lines...)
	out[cursorLine] = line[:start] + "/" + item.Value + " " + line[cursorCol:]
	return out, cursorLine, start + 1 + len(item.Value) + 1
}

func acProbes() []acProbe {
	names := []string{"alpha", "alpine", "beta", "build", "clear", "compact", "copy", "debug", "diff", "export", "fork", "help", "import", "login", "logout", "model", "name", "new", "quit", "reload", "resume", "session", "settings", "share", "tree", "a-command-name-that-is-longer-than-thirty-two-columns", "日本語のコマンド名", "x"}
	descriptions := []string{"", "Short", "A considerably longer description that has to be clipped at narrow widths", "日本語の説明 wide chars", "Tabs\tand\x1b[1mansi\x1b[0m"}
	random := rand.New(rand.NewSource(11))
	// Moving the cursor with the popup open and then accepting is left out: Pig re-queries the provider at the new cursor (AutocompleteAccept), Pi applies the stale popup there (Q10).
	keys := []string{"/", "a", "b", "c", "m", "o", "l", "x", " ", "\x1b[A", "\x1b[B", "\x1b[A", "\x1b[B", "\t", "\x1b", "\x7f", "\x1b[5~", "\x1b[6~"}
	widths := []int{1, 2, 3, 4, 5, 7, 10, 20, 33, 40, 60, 80, 120}
	var probes []acProbe
	for range 800 {
		count := []int{0, 1, 2, 6, 12, 25, 40}[random.Intn(7)]
		commands := make([]acCommand, 0, count)
		for i := range count {
			commands = append(commands, acCommand{Name: names[(i*7+random.Intn(3))%len(names)], Description: descriptions[random.Intn(len(descriptions))]})
		}
		probe := acProbe{Theme: []string{"dark", "light"}[random.Intn(2)], PaddingX: random.Intn(5), Commands: commands}
		if random.Intn(2) == 0 {
			v := []int{1, 3, 5, 8, 20, 50}[random.Intn(6)]
			probe.MaxVis = &v
		}
		// Pig clips a popup row wider than the content width and draws the end-of-line cursor over the last cell at width 1 where Pi overflows
		// (D66, Q8), so the corpus keeps at least two content cells.
		pick := func() int {
			for {
				w := widths[random.Intn(len(widths))]
				if w-2*min(probe.PaddingX, max(0, (w-1)/2)) >= 2 {
					return w
				}
			}
		}
		width := pick()
		for range 4 + random.Intn(14) {
			if random.Intn(14) == 0 {
				v := 1 + random.Intn(25)
				probe.Ops = append(probe.Ops, acOp{Max: &v, Width: width})
				continue
			}
			if random.Intn(8) == 0 {
				width = pick()
			}
			k := keys[random.Intn(len(keys))]
			probe.Ops = append(probe.Ops, acOp{Keys: &k, Width: width})
		}
		probes = append(probes, probe)
	}
	// Column layout: names around the 12 and 32 column limits with descriptions, at wide and narrow widths.
	for _, nameLen := range []int{1, 11, 12, 13, 31, 32, 33, 40} {
		for _, width := range []int{40, 60, 100} {
			name := strings.Repeat("n", nameLen)
			commands := []acCommand{{Name: name, Description: "the description column"}, {Name: "short", Description: "another description"}, {Name: name + "z"}}
			slash, down := "/", "\x1b[B"
			probes = append(probes, acProbe{Theme: "dark", Commands: commands, Ops: []acOp{{Keys: &slash, Width: width}, {Keys: &down, Width: width}}})
		}
	}
	return probes
}

func runACProbe(probe acProbe) []acFrame {
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetTheme(probe.Theme)
	SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), nil))
	e := NewEditor()
	e.BorderColor = func(text string) string { return ActiveTheme().Fg("borderMuted", text) }
	e.SetFocused(true)
	e.SetPaddingX(probe.PaddingX)
	if probe.MaxVis != nil {
		e.SetAutocompleteMaxVisible(*probe.MaxVis)
	}
	e.SetAutocomplete(acProvider{commands: probe.Commands})
	var frames []acFrame
	for _, op := range probe.Ops {
		switch {
		case op.Keys != nil:
			e.HandleInput(*op.Keys)
		case op.Max != nil:
			e.SetAutocompleteMaxVisible(*op.Max)
		}
		frames = append(frames, acFrame{Text: e.Text(), Rows: e.Render(op.Width)})
	}
	return frames
}

// Editor.render (components/editor.ts) with the coding-agent editor theme and a slash-command provider against pinned Pi: typing a slash opens the
// popup under the frame, up/down/page/home/end and tab move and accept, escape closes it, backspace refilters, the popup's window, descriptions,
// scroll counter and no-match state at widths 4 to 120 with padding 0 to 2 and a visible limit of 1 to 50, in both themes.
func TestEditorAutocompleteRenderMatchesPi(t *testing.T) {
	probes := acProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/editor_autocomplete_render.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]acFrame
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps, previousTheme, previousKeys := GetCapabilities(), ActiveTheme(), GetKeybindings()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
		SetKeybindings(previousKeys)
	})
	failures, popups := 0, 0
	for i, probe := range probes {
		got := runACProbe(probe)
		for _, f := range got {
			if len(f.Rows) > 3 {
				popups++
				break
			}
		}
		if !reflect.DeepEqual(got, expected[i]) {
			if failures++; failures <= 3 {
				for step := range got {
					if step >= len(expected[i]) || !reflect.DeepEqual(got[step], expected[i][step]) {
						t.Errorf("probe %d step %d ops %s theme=%s paddingX=%d max=%v commands=%d:\n  Pig %q %q\n  Pi  %q %q", i, step, mustJSON(probe.Ops[:step+1]), probe.Theme, probe.PaddingX, probe.MaxVis, len(probe.Commands), got[step].Text, got[step].Rows, expected[i][step].Text, expected[i][step].Rows)
						break
					}
				}
			}
		}
	}
	if failures > 3 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	if popups < len(probes)/4 {
		t.Errorf("only %d of %d probes showed a popup", popups, len(probes))
	}
}

// TestEditorAutocompleteRenderProbeDump prints the corpus for the Pi side of the editor-autocomplete-render parity scenario.
func TestEditorAutocompleteRenderProbeDump(t *testing.T) {
	fmt.Printf("acrender-probes:%s\n", mustJSON(acProbes()))
}

// TestEditorAutocompleteRenderParity prints Pig's frames for the corpus, one JSON line per probe, for the editor-autocomplete-render parity scenario.
func TestEditorAutocompleteRenderParity(t *testing.T) {
	previousCaps, previousTheme, previousKeys := GetCapabilities(), ActiveTheme(), GetKeybindings()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
		SetKeybindings(previousKeys)
	})
	for _, probe := range acProbes() {
		var line bytes.Buffer
		encoder := json.NewEncoder(&line)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(runACProbe(probe)); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("acrender-observation:%s", line.String())
	}
}

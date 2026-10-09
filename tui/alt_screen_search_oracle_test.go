package tui

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type altSearchOp struct {
	Keys   *string `json:"keys,omitempty"`
	Result *[2]int `json:"result,omitempty"`
	Hover  *int    `json:"hover,omitempty"`
	Render *int    `json:"render,omitempty"`
	At     *[2]int `json:"at,omitempty"`
}

type altSearchProbe struct {
	Focused  bool                `json:"focused"`
	Bindings map[string][]string `json:"bindings,omitempty"`
	Ops      []altSearchOp       `json:"ops"`
}

type altSearchStep struct {
	Changes   []string `json:"changes"`
	Rendered  []string `json:"rendered"`
	Direction *int     `json:"direction"`
	Changed   *bool    `json:"changed"`
}

// alt-screen-search.ts against pinned pi-tui: handleInput edits the query and reports only a changed query, and render lays out the
// query, result counter, navigation buttons (full labels, then arrows only, then none as the width shrinks), the hovered style and
// the Unbound/multi-key labels; getNavigationDirectionAt and setHoveredNavigationDirection agree for every cell.
func TestAltScreenSearchComponentMatchesPi(t *testing.T) {
	bindings := []map[string][]string{
		nil,
		{"tui.altScreen.searchPrevious": {"alt+p"}, "tui.altScreen.searchNext": {"ctrl+shift+n"}},
		{"tui.altScreen.searchPrevious": {}, "tui.altScreen.searchNext": {"f3", "enter"}},
		{"tui.altScreen.searchPrevious": {"shift+f3"}, "tui.altScreen.searchNext": {"a very long key name+x"}},
	}
	typed := []string{"a", "b", "z", "é", "日", "😀", " ", "ab", "x y", "\x7f", "\x7f", "\x01", "\x05", "\x17", "\x15", "\x0b", "\x19", "\x1b[D", "\x1b[C", "\x1b[H", "\x1b[F", "\x1b[3~", "\x1b[200~paste me\x1b[201~", "\x1b[1;5D", "\x1b[1;5C", "\x1f", "\r", "\x1b"}
	rng := rand.New(rand.NewSource(20261012))
	str := func(s string) *string { return &s }
	pair := func(a, b int) *[2]int { return &[2]int{a, b} }
	num := func(n int) *int { return &n }
	var probes []altSearchProbe
	for _, b := range bindings {
		for _, focused := range []bool{true, false} {
			for range 60 {
				probe := altSearchProbe{Focused: focused, Bindings: b}
				for range 6 + rng.Intn(24) {
					switch r := rng.Intn(10); {
					case r < 4:
						probe.Ops = append(probe.Ops, altSearchOp{Keys: str(typed[rng.Intn(len(typed))])})
					case r == 4:
						probe.Ops = append(probe.Ops, altSearchOp{Result: pair(rng.Intn(7)-1, rng.Intn(120))})
					case r == 5:
						probe.Ops = append(probe.Ops, altSearchOp{Hover: num(rng.Intn(3) - 1)})
					case r == 6:
						row := 2
						if rng.Intn(4) == 0 {
							row = rng.Intn(5)
						}
						probe.Ops = append(probe.Ops, altSearchOp{Render: num(30 + rng.Intn(51))}, altSearchOp{At: pair(row, rng.Intn(70)-2)})
					default:
						probe.Ops = append(probe.Ops, altSearchOp{Render: num(rng.Intn(82) - 1)})
					}
				}
				probe.Ops = append(probe.Ops, altSearchOp{Render: num(12)}, altSearchOp{Render: num(40)}, altSearchOp{Render: num(80)})
				probes = append(probes, probe)
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/alt_screen_search.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]altSearchStep
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousKeys, previousKitty, previousCaps := GetKeybindings(), IsKittyProtocolActive(), GetCapabilities()
	SetKittyProtocolActive(false)
	t.Cleanup(func() {
		SetKeybindings(previousKeys)
		SetKittyProtocolActive(previousKitty)
		SetCapabilities(previousCaps)
	})
	failures, changes, hovers, hits, narrow := 0, 0, 0, 0, 0
	for index, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		user := map[string][]KeyID{}
		for action, keys := range probe.Bindings {
			user[action] = []KeyID{}
			for _, key := range keys {
				user[action] = append(user[action], KeyID(key))
			}
		}
		restoreKeybindingsAfterTest(t)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), user))
		var seen []string
		component := NewAltScreenSearchComponent(func(query string) { seen = append(seen, markLoneSurrogatesOracle(query)) }, func(text string, hovered bool) string {
			if hovered {
				return "<" + text + ">"
			}
			return "[" + text + "]"
		})
		component.SetFocused(probe.Focused)
		for step, op := range probe.Ops {
			seen = []string{}
			got := altSearchStep{}
			switch {
			case op.Keys != nil:
				component.HandleInput(*op.Keys)
			case op.Result != nil:
				component.SetResult(op.Result[0], op.Result[1])
			case op.Hover != nil:
				changed := component.SetHoveredNavigationDirection(*op.Hover)
				got.Changed = &changed
				hovers++
			case op.Render != nil:
				lines := component.Render(*op.Render)
				got.Rendered = make([]string, len(lines))
				for i, line := range lines {
					got.Rendered[i] = markLoneSurrogatesOracle(line)
				}
				if *op.Render < 30 {
					narrow++
				}
			case op.At != nil:
				direction := component.GetNavigationDirectionAt(op.At[0], op.At[1])
				got.Direction = &direction
				if direction != 0 {
					hits++
				}
			}
			got.Changes = seen
			changes += len(seen)
			want := expected[index][step]
			if want.Changes == nil {
				want.Changes = []string{}
			}
			if !reflect.DeepEqual(got, want) {
				failures++
				if failures <= 5 {
					t.Errorf("probe %d op %d %+v bindings=%v focused=%v\nPig %+v\nPi  %+v", index, step, op, probe.Bindings, probe.Focused, got, want)
				}
				break
			}
		}
	}
	if changes == 0 || hovers == 0 || hits == 0 || narrow == 0 {
		t.Errorf("probes never reached changes=%d hovers=%d hits=%d narrow renders=%d", changes, hovers, hits, narrow)
	}
	t.Logf("%d probes, %d failures; changes %d hovers %d button hits %d narrow renders %d", len(probes), failures, changes, hovers, hits, narrow)
}

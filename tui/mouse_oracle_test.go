package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type mouseOracleItem struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type mouseOracleEvent struct {
	Type       string `json:"type"`
	Button     string `json:"button"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	ScreenX    int    `json:"screenX"`
	ScreenY    int    `json:"screenY"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	WheelDelta int    `json:"wheelDelta,omitempty"`
	ClickCount int    `json:"clickCount,omitempty"`
	Child      int    `json:"child"`
	Filter     string `json:"filter,omitempty"`
}

type mouseOracleChild struct {
	Kind       string            `json:"kind"`
	Text       string            `json:"text,omitempty"`
	N          int               `json:"n"`
	Items      []mouseOracleItem `json:"items,omitempty"`
	MaxVisible int               `json:"maxVisible,omitempty"`
}

type mouseOracleScenario struct {
	Kind        string             `json:"kind"`
	Width       int                `json:"width"`
	Items       []mouseOracleItem  `json:"items"`
	MaxVisible  int                `json:"maxVisible"`
	Filter      string             `json:"filter"`
	Preselect   int                `json:"preselect"`
	Fallback    string             `json:"fallback"`
	Value       string             `json:"value"`
	Events      []mouseOracleEvent `json:"events"`
	Children    []mouseOracleChild `json:"children"`
	PaddingX    int                `json:"paddingX"`
	PaddingY    int                `json:"paddingY"`
	RenderFirst bool               `json:"renderFirst"`
}

type mouseOracleTarget struct {
	OriginX     int     `json:"originX"`
	OriginY     int     `json:"originY"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	FocusTarget *string `json:"focusTarget"`
	Component   *string `json:"component,omitempty"`
}

type mouseOracleResult struct {
	Handled *bool              `json:"handled"`
	Capture *bool              `json:"capture"`
	Focus   *bool              `json:"focus"`
	Render  *bool              `json:"render"`
	Target  *mouseOracleTarget `json:"target,omitempty"`
}

type mouseOracleOut struct {
	Result *mouseOracleResult `json:"result"`
	Calls  []string           `json:"calls"`
}

type mouseOracleRecord struct {
	Frames [][]string       `json:"frames"`
	Outs   []mouseOracleOut `json:"outs"`
}

func mouseOracleScenarios() []mouseOracleScenario {
	rng := rand.New(rand.NewPCG(12, 5))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	values := []string{"alpha", "Beta", "gamma", "delta", "epsilon", "ab", "AB", "a/b", "日本語", "zeta-long-value-name", "x"}
	var scenarios []mouseOracleScenario
	for range 900 {
		scenario := mouseOracleScenario{Kind: pick([]string{"selectlist", "selectlist", "region", "input", "container", "container", "box", "box"}), RenderFirst: rng.IntN(4) != 0, PaddingX: rng.IntN(4), PaddingY: rng.IntN(3), Width: []int{4, 12, 24, 40, 80}[rng.IntN(5)], MaxVisible: []int{1, 2, 3, 5, 8}[rng.IntN(5)], Preselect: rng.IntN(5)}
		for range rng.IntN(14) {
			item := mouseOracleItem{Value: pick(values), Label: pick([]string{"Alpha", "beta label", "G", "日本語ラベル", "A much longer label that must be truncated"})}
			if rng.IntN(2) == 0 {
				item.Description = pick([]string{"desc", "a longer description text", "日本語", ""})
			}
			scenario.Items = append(scenario.Items, item)
		}
		if scenario.Items == nil {
			scenario.Items = []mouseOracleItem{}
		}
		if rng.IntN(4) == 0 {
			scenario.Filter = pick([]string{"a", "al", "b", "g", "zz", "A"})
		}
		scenario.Children = []mouseOracleChild{}
		for range rng.IntN(5) {
			switch rng.IntN(4) {
			case 3:
				scenario.Children = append(scenario.Children, mouseOracleChild{Kind: "input", Text: pick([]string{"", "hello", "hello world long text", "日本語"})})
			case 0:
				scenario.Children = append(scenario.Children, mouseOracleChild{Kind: "text", Text: pick([]string{"one", "two\nlines", "wrap this longer text across the narrow width", "", "日本語\nx\ny"})})
			case 1:
				scenario.Children = append(scenario.Children, mouseOracleChild{Kind: "spacer", N: rng.IntN(3)})
			default:
				child := mouseOracleChild{Kind: "list", MaxVisible: 1 + rng.IntN(4), Items: []mouseOracleItem{}}
				for range rng.IntN(6) {
					child.Items = append(child.Items, mouseOracleItem{Value: pick(values), Label: pick([]string{"Alpha", "beta", "G"})})
				}
				scenario.Children = append(scenario.Children, child)
			}
		}
		scenario.Fallback = pick([]string{"none", "handled", "focus", "capture", "norender", "render"})
		scenario.Value = pick([]string{"", "hello", "hello world", "日本語テキスト", "a😀b", strings.Repeat("long ", 30), "e\u0301x"})
		height := 1 + rng.IntN(8)
		for range 1 + rng.IntN(12) {
			if rng.IntN(8) == 0 {
				scenario.Events = append(scenario.Events, mouseOracleEvent{Type: "filter", Child: rng.IntN(5), Filter: pick([]string{"", "a", "zz", "b", "al"})})
				continue
			}
			event := mouseOracleEvent{Type: pick([]string{"press", "release", "move", "drag", "click", "wheel", "click", "press"}), Button: pick([]string{"left", "left", "left", "right", "middle", "none"}), Width: scenario.Width, Height: height}
			event.X = rng.IntN(scenario.Width+4) - 1
			event.Y = rng.IntN(height+3) - 1
			event.ScreenX, event.ScreenY = event.X+rng.IntN(3), event.Y+rng.IntN(3)
			if event.Type == "wheel" {
				event.WheelDelta = []int{-3, -1, 1, 2, 0}[rng.IntN(5)]
			}
			if event.Type == "click" {
				event.ClickCount = 1 + rng.IntN(3)
			}
			scenario.Events = append(scenario.Events, event)
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

func mouseOracleTheme() SelectListTheme {
	wrap := func(tag string) func(string) string {
		return func(text string) string { return "<" + tag + ">" + text + "</" + tag + ">" }
	}
	return SelectListTheme{SelectedPrefix: wrap("sp"), SelectedText: wrap("st"), Description: wrap("d"), ScrollInfo: wrap("si"), NoMatch: wrap("nm")}
}

func runMouseOracleWithPig(scenario mouseOracleScenario) mouseOracleRecord {
	var calls []string
	makeList := func() *SelectList {
		items := make([]SelectItem, len(scenario.Items))
		for i, item := range scenario.Items {
			items[i] = SelectItem(item)
		}
		list := NewSelectList(items, scenario.MaxVisible, mouseOracleTheme())
		list.OnSelect = func(item SelectItem) { calls = append(calls, "select:"+item.Value) }
		list.OnSelectionChange = func(item SelectItem) { calls = append(calls, "change:"+item.Value) }
		if scenario.Filter != "" {
			list.SetFilter(scenario.Filter)
		}
		for range scenario.Preselect {
			list.HandleInput("\x1b[B")
		}
		calls = nil
		return list
	}
	var component interface {
		Render(width int) []string
		HandleMouse(TuiMouseEvent) *TuiMouseDispatchResult
	}
	var childList *SelectList
	var kids []Component
	switch scenario.Kind {
	case "selectlist":
		component = makeList()
	case "region":
		childList = makeList()
		fallback := map[string]func() *TuiMouseEventResult{
			"none":     func() *TuiMouseEventResult { return nil },
			"handled":  func() *TuiMouseEventResult { return &TuiMouseEventResult{Handled: true} },
			"focus":    func() *TuiMouseEventResult { return &TuiMouseEventResult{Focus: true} },
			"capture":  func() *TuiMouseEventResult { return &TuiMouseEventResult{Capture: true} },
			"norender": func() *TuiMouseEventResult { no := false; return &TuiMouseEventResult{Handled: true, Render: &no} },
			"render":   func() *TuiMouseEventResult { yes := true; return &TuiMouseEventResult{Handled: true, Render: &yes} },
		}[scenario.Fallback]
		component = NewMouseRegion(childList, func(event TuiMouseEvent) *TuiMouseEventResult {
			calls = append(calls, "fallback:"+string(event.Type))
			return fallback()
		})
	case "container", "box":
		kids = nil
		for _, child := range scenario.Children {
			switch child.Kind {
			case "text":
				kids = append(kids, NewPaddedText(child.Text, 0, 0, nil))
			case "spacer":
				kids = append(kids, NewSpacer(child.N))
			case "input":
				input := NewInput(InputOptions{})
				input.SetFocused(true)
				input.SetValue(child.Text)
				kids = append(kids, input)
			default:
				items := make([]SelectItem, len(child.Items))
				for i, item := range child.Items {
					items[i] = SelectItem{Value: item.Value, Label: item.Label}
				}
				list := NewSelectList(items, child.MaxVisible, mouseOracleTheme())
				list.OnSelect = func(item SelectItem) { calls = append(calls, "select:"+item.Value) }
				list.OnSelectionChange = func(item SelectItem) { calls = append(calls, "change:"+item.Value) }
				kids = append(kids, list)
			}
		}
		if scenario.Kind == "box" {
			box := NewPaddedBox(scenario.PaddingX, scenario.PaddingY, nil)
			for _, kid := range kids {
				box.AddChild(kid)
			}
			component = box
		} else {
			container := NewContainer()
			for _, kid := range kids {
				container.Add(kid)
			}
			component = container
		}
	default:
		input := NewInput(InputOptions{})
		input.SetFocused(true)
		input.SetValue(scenario.Value)
		component = input
	}
	first := []string{}
	if scenario.RenderFirst || (scenario.Kind != "container" && scenario.Kind != "box") {
		first = component.Render(scenario.Width)
	}
	record := mouseOracleRecord{Frames: [][]string{first}}
	for _, event := range scenario.Events {
		calls = nil
		if event.Type == "filter" {
			if event.Child < len(kids) {
				if list, ok := kids[event.Child].(*SelectList); ok {
					list.SetFilter(event.Filter)
				}
			}
			record.Outs = append(record.Outs, mouseOracleOut{Calls: []string{}})
			record.Frames = append(record.Frames, record.Frames[len(record.Frames)-1])
			continue
		}
		result := component.HandleMouse(TuiMouseEvent{Type: TuiMouseEventType(event.Type), Button: TuiMouseButton(event.Button), X: event.X, Y: event.Y, ScreenX: event.ScreenX, ScreenY: event.ScreenY, Width: event.Width, Height: event.Height, WheelDelta: event.WheelDelta, ClickCount: event.ClickCount})
		out := mouseOracleOut{Calls: append([]string{}, calls...)}
		if result != nil {
			flag := func(b bool) *bool {
				if !b {
					return nil
				}
				return &b
			}
			out.Result = &mouseOracleResult{Handled: flag(result.Handled), Capture: flag(result.Capture), Focus: flag(result.Focus), Render: result.Render}
			if (scenario.Kind == "container" || scenario.Kind == "box") && result.Target.Component != nil {
				label := func(c Component) *string {
					which := "other"
					if c == component.(Component) {
						which = "self"
					}
					for i, kid := range kids {
						if c == kid {
							which = fmt.Sprintf("child:%d", i)
						}
					}
					return &which
				}
				target := &mouseOracleTarget{Component: label(result.Target.Component), OriginX: result.Target.OriginX, OriginY: result.Target.OriginY, Width: result.Target.Width, Height: result.Target.Height}
				if result.FocusTarget != nil {
					target.FocusTarget = label(result.FocusTarget)
				}
				out.Result.Target = target
			}
			if scenario.Kind == "region" && result.Target.Component != nil {
				target := &mouseOracleTarget{OriginX: result.Target.OriginX, OriginY: result.Target.OriginY, Width: result.Target.Width, Height: result.Target.Height}
				if result.FocusTarget != nil {
					which := "other"
					if result.FocusTarget == Component(childList) {
						which = "child"
					} else if result.FocusTarget == Component(component.(Component)) {
						which = "self"
					}
					target.FocusTarget = &which
				}
				out.Result.Target = target
			}
		}
		record.Outs = append(record.Outs, out)
		record.Frames = append(record.Frames, component.Render(scenario.Width))
	}
	return record
}

// handleMouse of SelectList, Input and MouseRegion against pinned Pi over 900 seeded scenarios: press, release, move, drag, click and wheel events with every button, inside and
// outside the component, including the wheel delta and click count; the result flags (handled, capture, focus, render), the callbacks that fired (selection change, select, the
// region's fallback), the dispatch target of a region and every frame after each event must agree.
func TestMouseHandlersMatchPi(t *testing.T) {
	scenarios := mouseOracleScenarios()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/mouse.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []mouseOracleRecord
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, scenario := range scenarios {
		got := runMouseOracleWithPig(scenario)
		if reflect.DeepEqual(normalizeMouseRecord(got), normalizeMouseRecord(expected[i])) {
			continue
		}
		if failures++; failures > 4 {
			continue
		}
		children, _ := json.Marshal(scenario.Children)
		report := fmt.Sprintf("scenario %d (%s, width %d, padding %d,%d, render first %v, children %s)", i, scenario.Kind, scenario.Width, scenario.PaddingX, scenario.PaddingY, scenario.RenderFirst, children)
		if !reflect.DeepEqual(normalizeMouseFrame(got.Frames[0]), normalizeMouseFrame(expected[i].Frames[0])) {
			report += fmt.Sprintf("\n  first frame\n  Pig %q\n  Pi  %q", got.Frames[0], expected[i].Frames[0])
		}
		for k := range got.Outs {
			g, _ := json.Marshal(got.Outs[k])
			e, _ := json.Marshal(expected[i].Outs[k])
			if !bytes.Equal(g, e) {
				ev, _ := json.Marshal(scenario.Events[k])
				report += fmt.Sprintf("\n  event %d %s\n  Pig %s\n  Pi  %s", k, ev, g, e)
				break
			}
			if !reflect.DeepEqual(normalizeMouseFrame(got.Frames[k+1]), normalizeMouseFrame(expected[i].Frames[k+1])) {
				report += fmt.Sprintf("\n  frame after event %d\n  Pig %q\n  Pi  %q", k, got.Frames[k+1], expected[i].Frames[k+1])
				break
			}
		}
		t.Error(report)
	}
	if failures > 4 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}

// normalizeMouseFrame treats no lines and one empty line as different only when Pi's differ: both are an empty render.
func normalizeMouseFrame(lines []string) []string {
	if len(lines) == 1 && lines[0] == "" {
		return []string{}
	}
	return lines
}

func normalizeMouseRecord(r mouseOracleRecord) mouseOracleRecord {
	frames := make([][]string, len(r.Frames))
	for i, f := range r.Frames {
		frames[i] = normalizeMouseFrame(f)
	}
	r.Frames = frames
	return r
}

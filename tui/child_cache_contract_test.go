package tui

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi has no per-child render cache: Container.render calls child.render(width) for every child on every frame (packages/tui/src/tui.ts Container.render),
// so a Pi component cannot be stale because of its parent. Pig keeps a per-child cache and a settled-child scan (container_settled.go) as an
// optimization, which must never show a child Pi would have redrawn. The tests below hold it to that: whatever a parent container shows for a
// component after a mutation is what the component renders by itself.

// contractWidth is the width every contract render uses.
const contractWidth = 80

// contractWrapper is a component that embeds a Container and renders by it, as a selector, a message card or a dialog does; its own invalidation
// flag is the Container's, so only the Container's answer to IsDirty tells a parent that something beneath it changed.
type contractWrapper struct{ Container }

// renderThroughParent returns what a parent Container renders, the way the transcript does.
func renderThroughParent(parent *Container, width int) []string {
	return slices.Clone(parent.Render(width))
}

func TestModelSelectorSetErrorReachesAParentContainersRender(t *testing.T) {
	ui, _ := newModelSelectorTUI(t)
	current, browsed := selectorTestModel("current-model", "Current Model"), selectorTestModel("browsed-model", "Browsed Model")
	selector := NewModelSelectorComponent(ui, current, &fakeModelRuntime{models: []*ai.Model{browsed, current}}, nil, func(*ai.Model) {}, func() {}, "", nil, nil)
	t.Cleanup(selector.Dispose)
	parent := NewContainer(selector)
	_ = renderThroughParent(parent, 120)
	selector.SetError("catalog refresh failed")
	got, want := plain(renderThroughParent(parent, 120)), plain(selector.Render(120))
	if !slices.Equal(got, want) {
		t.Fatalf("parent render after SetError:\n%q\ndirect render:\n%q", got, want)
	}
}

// The model picker is the editor slot's child (InteractiveMode.showModelSelector), and the owner-loop task that ends a timed-out catalog refresh
// calls rejectCatalogRefresh and requests a render: the next frame must show the timeout message in place of the refresh status.
func TestModelSelectorRefreshTimeoutShowsInTheNextFrame(t *testing.T) {
	ui, _ := newModelSelectorTUI(t)
	current := selectorTestModel("current-model", "Current Model")
	selector := NewModelSelectorComponent(ui, current, &fakeModelRuntime{models: []*ai.Model{current}}, nil, func(*ai.Model) {}, func() {}, "", nil, nil)
	t.Cleanup(selector.Dispose)
	editorSlot := NewContainer()
	ui.AddChild(editorSlot)
	editorSlot.SetChildren(selector)
	if frame := strings.Join(plain(ui.RenderSnapshot(120)), "\n"); !strings.Contains(frame, "Refreshing model catalogs") {
		t.Fatalf("first frame lacks the refresh status:\n%s", frame)
	}
	selector.rejectCatalogRefresh(true)
	if frame := strings.Join(plain(ui.RenderSnapshot(120)), "\n"); !strings.Contains(frame, "Model refresh timed out") {
		t.Fatalf("frame after the timeout lacks the message:\n%s", frame)
	}
}

func TestSettingsListUpdateValueReachesAParentContainersRender(t *testing.T) {
	list := NewSettingsList([]SettingItem{{ID: "a", Label: "A", CurrentValue: "on", Values: []string{"on", "off"}}}, 5, SettingsListThemeFor(ActiveTheme()), func(string, string) {}, func() {})
	parent := NewContainer(list)
	_ = parent.Render(80)
	list.UpdateValue("a", "off")
	got, want := plain(parent.Render(80)), plain(list.Render(80))
	if !slices.Equal(got, want) {
		t.Fatalf("parent render:\n%q\ndirect render:\n%q", got, want)
	}
}

// contractCall is one call of a mutator whose arguments the walker cannot synthesize.
type contractCall struct {
	method string
	args   []any
}

// contractFixture builds one exported component type in a state where its mutators have something to show.
type contractFixture struct {
	build  func(t *testing.T) Component
	extras []contractCall
	// ownRender names the methods the walker compares with the component's own render and not with contractReference, each with the reason: a rebuild
	// of what the component draws (which invalidation runs) does not keep what the method changed.
	ownRender map[string]string
}

// contractOwnChildren is the reason of a method that adds or removes children of a component that builds its own: its rebuild replaces them.
const contractOwnChildren = "the component builds its children from its state on every rebuild, which replaces the ones this method added or removed"

type contractNode struct {
	id, label string
	kids      []TreeNode
}

func (n contractNode) NodeID() string           { return n.id }
func (n contractNode) NodeLabel() string        { return n.label }
func (n contractNode) NodeChildren() []TreeNode { return n.kids }

func contractTUI(t *testing.T) TUI {
	ui, _ := newModelSelectorTUI(t)
	return ui
}

func contractFixtures() map[string]contractFixture {
	noop := func() {}
	return map[string]contractFixture{
		"AssistantMessageComponent": {
			// A thinking block hidden behind its label and an answer: SetHideThinkingBlock, SetHiddenThinkingLabel and SetOutputPad each change what it draws.
			build: func(*testing.T) Component {
				return NewAssistantMessageComponent(&AssistantMessage{Content: []AssistantContentBlock{{Type: "thinking", Thinking: "pondering"}, {Type: "text", Text: "first"}}}, true, nil, "", nil, nil, "id", "")
			},
			extras: []contractCall{
				{"UpdateContent", []any{&AssistantMessage{Content: []AssistantContentBlock{{Type: "text", Text: "second answer"}, {Type: "thinking", Thinking: "hmm"}}}}},
				{"UpdateContent", []any{&AssistantMessage{StopReason: "error", ErrorMessage: "boom"}}},
				{"SetTerminalError", []any{"error", "boom"}},
				{"SetTerminalError", []any{"aborted", ""}},
			},
		},
		"AuthURL": {build: func(*testing.T) Component {
			return NewAuthURL("https://example.test/auth", func(string) error { return nil }, noop)
		}},
		"BashExecutionComponent": {
			build: func(t *testing.T) Component { return NewBashExecutionComponent("echo hi", contractTUI(t), false, 1) },
			extras: []contractCall{
				{"AppendOutput", []any{"line one\nline two\n"}},
				{"SetCompleteWithOutput", []any{new(int), false, false, "line one\nline two\ndone\n", ""}},
			},
		},
		"BorderedLoader": {build: func(t *testing.T) Component { return NewBorderedLoader(contractTUI(t), ActiveTheme(), "Working") }},
		"Box":            {build: func(*testing.T) Component { b := NewBox(); b.AddChild(NewText("inside")); return b }},
		"Container":      {build: func(*testing.T) Component { return NewContainer(NewText("a")) }},
		"BranchSummaryMessageComponent": {
			build: func(*testing.T) Component {
				return NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "branch summary"}, nil, 1, nil, "", "")
			},
			ownRender: map[string]string{"AddChild": contractOwnChildren, "RemoveChild": contractOwnChildren, "Clear": contractOwnChildren},
		},
		"CompactionSummaryMessageComponent": {
			build: func(*testing.T) Component {
				return NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: "compaction summary", TokensBefore: 1234}, nil, 1, nil, "", "")
			},
			ownRender: map[string]string{"AddChild": contractOwnChildren, "RemoveChild": contractOwnChildren, "Clear": contractOwnChildren},
		},
		"SkillInvocationMessageComponent": {
			build: func(*testing.T) Component {
				return NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "skill", Content: "skill body"}, nil, 1, nil, "", "")
			},
			ownRender: map[string]string{"AddChild": contractOwnChildren, "RemoveChild": contractOwnChildren, "Clear": contractOwnChildren},
		},
		"ConfigSelectorComponent": {build: func(*testing.T) Component {
			return NewConfigSelector([]*ResourceGroup{{Key: "k", Label: "Group", Scope: "user"}}, 20)
		}},
		"CustomMessageComponent": {
			build: func(*testing.T) Component {
				return NewCustomMessageComponent(&CustomMessage{CustomType: "card", Content: "body"}, func(_ *CustomMessage, o MessageRenderOptions) Component {
					return NewText(fmt.Sprintf("custom card expanded=%t pad=%d", o.Expanded, o.OutputPad))
				}, nil, 1, nil, "id", "")
			},
			ownRender: map[string]string{"Add": contractOwnChildren, "AddChild": contractOwnChildren, "Remove": contractOwnChildren, "RemoveChild": contractOwnChildren, "Clear": contractOwnChildren},
		},
		"CustomMessageComponent#default box": {
			build: func(*testing.T) Component {
				return NewCustomMessageComponent(&CustomMessage{CustomType: "card", Content: "body"}, nil, nil, 1, nil, "id", "")
			},
			ownRender: map[string]string{"Add": contractOwnChildren, "AddChild": contractOwnChildren, "Remove": contractOwnChildren, "RemoveChild": contractOwnChildren, "Clear": contractOwnChildren},
		},
		"DiffComponent": {build: func(*testing.T) Component { return NewDiffComponent("-a\n+b\n", "f.txt") }},
		"DynamicBorder": {build: func(*testing.T) Component { return NewDynamicBorder() }},
		"Editor":        {build: func(*testing.T) Component { return NewEditor() }},
		"ExtensionInputComponent": {build: func(*testing.T) Component {
			return NewExtensionInputComponent("Title", "placeholder", func(string) {}, noop)
		}},
		"ExtensionSelectorComponent": {build: func(*testing.T) Component {
			return NewExtensionSelectorComponent("Title", []string{"one", "two", "three"}, func(string) {}, noop)
		}},
		"FilterableList": {build: func(*testing.T) Component { return NewFilterableList("Items", []string{"alpha", "beta", "gamma"}) }},
		"HStack": {build: func(*testing.T) Component {
			return NewHStack([]StackChild{{Component: NewText("left")}, {Component: NewText("right")}}, StackOptions{})
		}},
		"LoginDialogComponent": {build: func(t *testing.T) Component {
			return NewLoginDialogComponent(contractTUI(t), "github-copilot", func(bool, string) {}, "")
		}},
		"Markdown": {build: func(*testing.T) Component { return NewMarkdown("# heading\n\nbody text") }},
		"ModelSelectorComponent": {build: func(t *testing.T) Component {
			ui, _ := newModelSelectorTUI(t)
			current, other := selectorTestModel("current-model", "Current Model"), selectorTestModel("other-model", "Other Model")
			selector := NewModelSelectorComponent(ui, current, &fakeModelRuntime{models: []*ai.Model{other, current}}, nil, func(*ai.Model) {}, noop, "", nil, nil)
			t.Cleanup(selector.Dispose)
			return selector
		}},
		"MouseRegion": {build: func(*testing.T) Component {
			return NewMouseRegion(NewText("region"), func(TuiMouseEvent) *TuiMouseEventResult { return nil })
		}},
		"OAuthSelectorComponent": {build: func(*testing.T) Component {
			return NewOAuthSelectorComponent("login", []OAuthProvider{{ID: "a", Name: "Alpha", AuthType: "oauth"}, {ID: "b", Name: "Beta", AuthType: "api_key"}}, func(string, string) {}, noop)
		}},
		"ScopedModelsList": {build: func(*testing.T) Component {
			return NewScopedModelsList(ScopedModelsConfig{AllModels: []ModelItem{{FullID: "p/a", Name: "A", Provider: "p"}, {FullID: "p/b", Name: "B", Provider: "p"}}})
		}},
		"ScrollView": {build: func(*testing.T) Component {
			return NewScrollView(NewText(strings.Repeat("a long line of scrolled text\n", 40)), ScrollViewOptions{})
		}},
		"SelectList": {build: func(*testing.T) Component {
			return NewSelectList([]SelectItem{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}}, 5, SelectListThemeFor(ActiveTheme()))
		}},
		"SelectSubmenuComponent": {build: func(*testing.T) Component {
			return NewSelectSubmenu("Title", "desc", []SelectItem{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}}, "a")
		}},
		"SettingsList": {build: func(*testing.T) Component {
			return NewSettingsList([]SettingItem{{ID: "a", Label: "A", CurrentValue: "on", Values: []string{"on", "off"}}, {ID: "b", Label: "B", CurrentValue: "x", Values: []string{"x", "y"}}}, 5, SettingsListThemeFor(ActiveTheme()), func(string, string) {}, noop)
		}},
		"ShowImagesSelectorComponent": {build: func(*testing.T) Component { return NewShowImagesSelectorComponent(true, func(bool) {}, noop) }},
		"Spacer":                      {build: func(*testing.T) Component { return NewSpacer(1) }},
		"StaticText":                  {build: func(*testing.T) Component { return NewStaticText([]string{"fixed"}) }},
		"Text":                        {build: func(*testing.T) Component { return NewText("hello") }},
		"TextInput":                   {build: func(*testing.T) Component { return NewTextInput("") }},
		"ThemeSelectorComponent": {build: func(*testing.T) Component {
			return NewThemeSelectorComponent("dark", func(string) {}, noop, func(string) {})
		}},
		"ThemedText": {
			build: func(*testing.T) Component { return NewThemedText(func() string { return "themed" }, 0, 0) },
			ownRender: map[string]string{
				"SetText": "the content is rebuilt from build on every invalidation, as Pi's themed-text.ts does, so a SetText does not survive one",
			},
		},
		"ThinkingBlock": {build: func(*testing.T) Component { return NewThinkingBlock(false) }},
		"ToolExecutionComponent": {
			build: func(t *testing.T) Component {
				return NewToolExecutionComponent("bash", "call-1", json.RawMessage(`{"command":"ls"}`), ToolExecutionOptions{}, nil, contractTUI(t), ".")
			},
			extras: []contractCall{
				{"SetResult", []any{"file one\nfile two\n", false, 2 * time.Second}},
				{"SetResult", []any{"failed", true, time.Second}},
			},
		},
		"TreeSelectorComponent": {build: func(*testing.T) Component {
			return NewTreeSelectorComponent("Tree", contractNode{id: "r", label: "root", kids: []TreeNode{contractNode{id: "a", label: "alpha"}, contractNode{id: "b", label: "beta"}}})
		}},
		"TruncatedText": {build: func(*testing.T) Component { return NewTruncatedText("a line that may be truncated", 0, 0) }},
		"UserMessageComponent": {
			build: func(*testing.T) Component { return NewUserMessageComponent("hello there", nil, 1, nil, "id", "") },
		},
		"UserMessageSelectorComponent": {build: func(*testing.T) Component {
			return NewUserMessageSelectorComponent([]UserMessageItem{{ID: "1", Text: "first"}, {ID: "2", Text: "second"}}, func(string) {}, noop, "")
		}},
		"VStack": {build: func(*testing.T) Component {
			return NewVStack([]StackChild{{Component: NewText("top")}, {Component: NewText("bottom")}}, StackOptions{})
		}},
		"VisualLinePreview": {build: func(*testing.T) Component {
			return NewVisualLinePreview(VisualLinePreviewOptions{Text: "a\nb\nc\nd", MaxVisualLines: 2, FormatHint: func(hidden int) string { return fmt.Sprintf("+%d", hidden) }})
		}},
	}
}

// contractStatic names the built component types no exported method of which changes what the component renders in the fixture's state, with the
// reason; the walk still requires the parent to match for every call, and fails when a type on the list starts to change.
var contractStatic = map[string]string{
	"DynamicBorder":     "a rule of the terminal's width with no state",
	"MouseRegion":       "forwards to a child it does not expose",
	"ScrollView":        "its offset is clamped to a viewport that only a surface's layout pass sets",
	"StaticText":        "fixed lines",
	"TruncatedText":     "constructor arguments only",
	"VisualLinePreview": "constructor options only",
}

// contractExempt names the exported component types the walker does not build, each with the reason it cannot show a stale child.
var contractExempt = map[string]string{
	"CancellableLoader":        "a Loader, which is exempted below",
	"StatusIndicator":          "a Loader, which is exempted below",
	"TuiAltScreen":             "a root renderer: it draws the screen and is never a child of a cached container",
	"TuiMainScreen":            "a root renderer: it draws the screen and is never a child of a cached container",
	"TuiSurface":               "a root renderer: it draws the screen and is never a child of a cached container",
	"AltScreenFlashContainer":  "drawn only by TuiAltScreen, which renders its flashes from its own timer",
	"AltScreenSearchComponent": "drawn only by TuiAltScreen as its search overlay, never a child of a cached container",
	"BaseComponent":            "the invalidation mixin; it renders nothing",
	"IdleStatus":               "renders blanks and has no state",
	"Image":                    "renders a terminal graphics escape whose bytes depend on the terminal's image protocol, which a test cannot choose",
	"Loader":                   "an animation of the wall clock, so two renders of one state differ; the cache contract for it is the IsDirty the loader owners test",
	"Stack":                    "the shared base of VStack and HStack, which are built above",
	"TreeList":                 "owned by TreeSelectorComponent, which is built above and has no standalone constructor",
	"UserMessageList":          "owned by UserMessageSelectorComponent, which is built above and has no standalone constructor",
}

// contractSkipped are the methods the walker never calls: those that are the contract itself, that end the component's life, or that start work on
// another goroutine or the wall clock.
func contractSkipped(name string) bool {
	switch name {
	case "Render", "Invalidate", "NeedsRedraw", "IsDirty", "Dispose", "Stop", "Start", "Close", "Wait", "Run", "Shutdown", "StopAnimation", "Flash":
		return true
	}
	return false
}

var contractStrings = []string{"", "a", "off", "changed text", "\x1b[B", "\r"}

// contractArgs returns the values the walker passes for a parameter of type t, or nil when it cannot make one.
func contractArgs(t reflect.Type) []reflect.Value {
	zero := func() []reflect.Value { return []reflect.Value{reflect.Zero(t)} }
	switch {
	case t == reflect.TypeFor[Component]():
		return []reflect.Value{reflect.ValueOf(Component(NewText("probe")))}
	case t == reflect.TypeFor[time.Duration]():
		return []reflect.Value{reflect.ValueOf(time.Duration(0)), reflect.ValueOf(1500 * time.Millisecond)}
	}
	switch t.Kind() {
	case reflect.String:
		out := make([]reflect.Value, len(contractStrings))
		for i, s := range contractStrings {
			out[i] = reflect.ValueOf(s).Convert(t)
		}
		return out
	case reflect.Bool:
		return []reflect.Value{reflect.ValueOf(true).Convert(t), reflect.ValueOf(false).Convert(t)}
	case reflect.Int, reflect.Int64:
		out := make([]reflect.Value, 0, 4)
		for _, n := range []int64{0, 1, 3, 5} {
			out = append(out, reflect.ValueOf(n).Convert(t))
		}
		return out
	case reflect.Float64:
		return []reflect.Value{reflect.ValueOf(0.0).Convert(t), reflect.ValueOf(0.5).Convert(t)}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.String {
			return []reflect.Value{reflect.Zero(t), reflect.ValueOf([]string{"a", "b"}).Convert(t)}
		}
		return zero()
	case reflect.Func:
		zeroFn := reflect.MakeFunc(t, func([]reflect.Value) []reflect.Value {
			out := make([]reflect.Value, t.NumOut())
			for i := range out {
				out[i] = reflect.Zero(t.Out(i))
			}
			return out
		})
		out := []reflect.Value{zeroFn}
		if t.NumIn() == 1 && t.NumOut() == 1 && t.In(0).Kind() == reflect.String && t.Out(0).Kind() == reflect.String {
			// A function that marks its argument, which a key made of a sample of its output (Box's background) tells from the zero function.
			out = append(out, reflect.MakeFunc(t, func(in []reflect.Value) []reflect.Value {
				return []reflect.Value{reflect.ValueOf("«" + in[0].String() + "»").Convert(t.Out(0))}
			}))
		}
		return out
	}
	return nil
}

// contractCalls lists the calls of every exported method of the component that the walker can make, in a stable order. A method with more than
// contractComboLimit argument combinations is sampled evenly.
const contractComboLimit = 24

type contractInvocation struct {
	name string
	args []reflect.Value
}

func contractCalls(x Component, extras []contractCall) []contractInvocation {
	rt := reflect.TypeOf(x)
	var out []contractInvocation
	for m := range rt.Methods() {
		m := m
		if contractSkipped(m.Name) || m.Type.IsVariadic() {
			continue
		}
		pools := make([][]reflect.Value, 0, m.Type.NumIn()-1)
		usable := true
		total := 1
		for p := 1; p < m.Type.NumIn(); p++ {
			pool := contractArgs(m.Type.In(p))
			if len(pool) == 0 {
				usable = false
				break
			}
			pools = append(pools, pool)
			total *= len(pool)
		}
		if !usable {
			continue
		}
		n := min(total, contractComboLimit)
		for k := range n {
			index := k * total / n
			args := make([]reflect.Value, len(pools))
			for p := len(pools) - 1; p >= 0; p-- {
				args[p] = pools[p][index%len(pools[p])]
				index /= len(pools[p])
			}
			out = append(out, contractInvocation{name: m.Name, args: args})
		}
	}
	for _, e := range extras {
		args := make([]reflect.Value, len(e.args))
		for i, a := range e.args {
			args[i] = reflect.ValueOf(a)
		}
		out = append(out, contractInvocation{name: e.method, args: args})
	}
	return out
}

// contractCall calls the method and reports whether it ran to the end: a method may panic on an argument its component has no use for, which the
// walker does not count against the component.
func (c contractInvocation) call(x Component) (ran bool) {
	defer func() {
		if recover() != nil {
			ran = false
		}
	}()
	method := reflect.ValueOf(x).MethodByName(c.name)
	if !method.IsValid() {
		return false
	}
	for _, result := range method.Call(c.args) {
		// A method that finishes on another goroutine returns the channel that reports it (AuthURL.Copy); one that waits for the user (a login
		// dialog's prompt) returns a channel that stays open, which the wait gives up on.
		if result.Kind() == reflect.Chan && result.Type().ChanDir()&reflect.RecvDir != 0 {
			reflect.Select([]reflect.SelectCase{{Dir: reflect.SelectRecv, Chan: result}, {Dir: reflect.SelectRecv, Chan: reflect.ValueOf(time.After(50 * time.Millisecond))}})
		}
	}
	return true
}

func (c contractInvocation) String() string {
	parts := make([]string, len(c.args))
	for i, a := range c.args {
		parts[i] = fmt.Sprintf("%#v", a.Interface())
	}
	return c.name + "(" + strings.Join(parts, ", ") + ")"
}

// contractContainer is the Container a component embeds or is, or nil.
func contractContainer(x Component) *Container {
	if c, ok := x.(*Container); ok {
		return c
	}
	v := reflect.ValueOf(x)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return nil
	}
	field := v.Elem().FieldByName("Container")
	if !field.IsValid() || field.Type() != reflect.TypeFor[Container]() {
		return nil
	}
	return field.Addr().Interface().(*Container)
}

// contractReference is what Pi would draw for x now, with no cache in the way. Pi's Container.render calls render on every child on every frame, and
// a component's invalidate drops its render caches and rebuilds what it baked from its state, so a component invalidated down its tree and rendered shows
// the state its mutators left whatever any flag, per-child cache or settled window holds. It changes x (it leaves x and its children dirty), so the walker
// takes it after it took the parent's frame.
func contractReference(x Component, width int) []string {
	var invalidate func(Component)
	invalidate = func(c Component) {
		c.Invalidate()
		if container := contractContainer(c); container != nil {
			for _, child := range container.Children() {
				invalidate(child)
			}
		}
	}
	invalidate(x)
	return slices.Clone(x.Render(width))
}

// contractWalk is the walk of one fixture in one layout, and the check of every mutator of it: it builds the component beneath a parent container
// (directly, or beneath a component that embeds a Container, as selectors and cards do), renders the parent, calls an exported method with the argument values
// of contractArgs, renders the parent again, and requires the parent to show contractReference. Each method starts from a component of its own, so a method
// that empties or ends the component (Clear, Remove) cannot hide the methods after it. It returns the first mismatch, and how many calls changed what the component draws.
func contractWalk(t *testing.T, fixture contractFixture, layout string) (changed int, err error) {
	lead := len(NewSpacer(1).Render(contractWidth))
	var methods []string
	for _, call := range contractCalls(fixture.build(t), fixture.extras) {
		if !slices.Contains(methods, call.name) {
			methods = append(methods, call.name)
		}
	}
	var current contractInvocation
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("after %s a render panicked: %v", current, r)
		}
	}()
	for _, method := range methods {
		x := fixture.build(t)
		root := NewContainer(NewSpacer(1))
		if layout == "child" {
			root.Add(x)
		} else {
			wrapper := &contractWrapper{}
			wrapper.Add(x)
			root.Add(wrapper)
		}
		_ = renderThroughParent(root, contractWidth)
		_ = renderThroughParent(root, contractWidth)
		previous := contractReference(x, contractWidth)
		_ = renderThroughParent(root, contractWidth)
		for _, call := range contractCalls(x, fixture.extras) {
			if call.name != method {
				continue
			}
			current = call
			if !call.call(x) {
				continue
			}
			got := renderThroughParent(root, contractWidth)
			reference := contractReference
			if _, own := fixture.ownRender[call.name]; own {
				reference = func(x Component, width int) []string { return slices.Clone(x.Render(width)) }
			}
			want := reference(x, contractWidth)
			if !slices.Equal(want, reference(x, contractWidth)) {
				// The component renders differently for one state (a clock, an animation): there is no render to compare.
				previous = want
				continue
			}
			if len(got) < lead || !slices.Equal(got[lead:], want) {
				return changed, fmt.Errorf("after %s the parent container shows\n%q\nbut Pi would draw (the component invalidated and rendered with no cache)\n%q", call, plain(got[min(lead, len(got)):]), plain(want))
			}
			if !slices.Equal(previous, want) {
				changed++
			}
			previous = want
			// A second render with nothing changed is what makes a container trust a settled child to report its own invalidations.
			_ = renderThroughParent(root, contractWidth)
		}
	}
	return changed, nil
}

// TestAParentContainerShowsWhatAnyExportedMutatorChanged is the guard on the per-child render cache: contractWalk over every exported component type in both
// layouts. A mutator that changes what a component draws without telling its parent fails here whatever the component is, as does one that does not
// rebuild what it draws from, so the next one cannot return unnoticed: a new exported component type must be added to contractFixtures or named in contractExempt.
func TestAParentContainerShowsWhatAnyExportedMutatorChanged(t *testing.T) {
	fixtures := contractFixtures()
	names := make([]string, 0, len(fixtures))
	for name := range fixtures {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fixture := fixtures[name]
		for _, layout := range []string{"child", "beneath an embedding component"} {
			t.Run(name+"/"+layout, func(t *testing.T) {
				changed, err := contractWalk(t, fixture, layout)
				if err != nil {
					t.Fatal(err)
				}
				if _, static := contractStatic[name]; changed == 0 && !static {
					t.Fatalf("no exported method changed what %s renders, so the walk proves nothing: give it a fixture whose mutators have something to show, or name it in contractStatic", name)
				}
				if _, static := contractStatic[name]; changed != 0 && static {
					t.Fatalf("%s is named in contractStatic but a method changed what it renders: remove it from the list", name)
				}
			})
		}
	}
}

// contractLeaf draws its text and, as a Text does, is cached by its parent until it is invalidated.
type contractLeaf struct {
	invalidatable
	text string
}

func (l *contractLeaf) Render(int) []string { return []string{l.text} }

// contractFaulty is a component with a mutator each way a component can be wrong: SetLeaf changes what a child beneath it draws without invalidating that
// child (a container whose own render is as stale as its parent's), SetLabel stores what it draws from and does not rebuild (Invalidate does, as a
// component that bakes its state into children does), and the sound variants do neither. The fault is a field, so one type is the mutant and its control.
type contractFaulty struct {
	Container
	leaf    *contractLeaf
	label   string
	faulty  string // "", "leaf" or "rebuild"
	rebuilt *Text
}

func newContractFaulty(faulty string) *contractFaulty {
	f := &contractFaulty{leaf: &contractLeaf{text: "leaf"}, label: "label", faulty: faulty}
	f.rebuilt = NewText("label")
	f.Add(f.leaf)
	f.Add(f.rebuilt)
	return f
}

func (f *contractFaulty) SetLeaf(text string) {
	f.leaf.text = text
	if f.faulty != "leaf" {
		f.leaf.Invalidate()
	}
}

func (f *contractFaulty) SetLabel(label string) {
	f.label = label
	if f.faulty != "rebuild" {
		f.rebuild()
	}
}

func (f *contractFaulty) Invalidate() {
	f.Container.Invalidate()
	f.rebuild()
}

func (f *contractFaulty) rebuild() { f.rebuilt.SetText(f.label) }

// TestTheWalkFailsOnAMutatorThatStalesWhatIsBeneathItOrDropsItsRebuild holds the walker's oracle to the mutants the component's own render cannot see: a
// component compared with its own Render agrees with a parent that is as stale as it is.
func TestTheWalkFailsOnAMutatorThatStalesWhatIsBeneathItOrDropsItsRebuild(t *testing.T) {
	for _, faulty := range []string{"", "leaf", "rebuild"} {
		fixture := contractFixture{build: func(*testing.T) Component { return newContractFaulty(faulty) }}
		for _, layout := range []string{"child", "beneath an embedding component"} {
			changed, err := contractWalk(t, fixture, layout)
			switch {
			case faulty == "" && err != nil:
				t.Errorf("sound component, %s: %v", layout, err)
			case faulty == "" && changed == 0:
				t.Errorf("sound component, %s: no call changed what it draws, so the walk proves nothing", layout)
			case faulty != "" && err == nil:
				t.Errorf("component with the %q fault, %s: the walk found nothing", faulty, layout)
			}
		}
	}
}

// contractComponentTypes returns the exported types of the parsed files that are components: a struct type that embeds a Container, BaseComponent or the
// invalidation mixin, or a type that declares Render (on a pointer or a value receiver), or a struct type that embeds a component type of the files, which
// it gets Render and the invalidation from by promotion.
func contractComponentTypes(files []*ast.File) map[string]bool {
	embeddedName := func(field *ast.Field) string {
		expr := field.Type
		if star, ok := expr.(*ast.StarExpr); ok {
			expr = star.X
		}
		if id, ok := expr.(*ast.Ident); ok {
			return id.Name
		}
		return ""
	}
	component := map[string]bool{}
	exported := map[string]bool{}
	embeds := map[string][]string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					exported[ts.Name.Name] = ts.Name.IsExported()
					if st, ok := ts.Type.(*ast.StructType); ok {
						for _, field := range st.Fields.List {
							if name := embeddedName(field); len(field.Names) == 0 && name != "" {
								embeds[ts.Name.Name] = append(embeds[ts.Name.Name], name)
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil || d.Name.Name != "Render" || len(d.Recv.List) == 0 {
					continue
				}
				recv := d.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				if id, ok := recv.(*ast.Ident); ok {
					component[id.Name] = true
				}
			}
		}
	}
	for name, kids := range embeds {
		for _, kid := range kids {
			if kid == "Container" || kid == "BaseComponent" || kid == "invalidatable" {
				component[name] = true
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for name, kids := range embeds {
			if component[name] {
				continue
			}
			for _, kid := range kids {
				if component[kid] {
					component[name], changed = true, true
					break
				}
			}
		}
	}
	out := map[string]bool{}
	for name := range component {
		if exported[name] {
			out[name] = true
		}
	}
	return out
}

// TestTheComponentTypeGuardSeesPromotedAndValueReceiverRender holds contractComponentTypes to the shapes a pointer-receiver Render on a struct that embeds a
// Container misses: Render promoted from an embedded component, at any depth and through a pointer, and Render on a value receiver.
func TestTheComponentTypeGuardSeesPromotedAndValueReceiverRender(t *testing.T) {
	const source = `package p
type Text struct{ x int }
func (t *Text) Render(w int) []string { return nil }
type Panel struct{ Container }
type Promoted struct{ *Text }
type Deep struct{ Promoted }
type ByValue struct{ x int }
func (ByValue) Render(w int) []string { return nil }
type base struct{ *Text }
type ViaUnexported struct{ base }
type unexportedEmbedder struct{ *Text }
type Plain struct{ x int }
type Holder struct{ text *Text }
`
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := contractComponentTypes([]*ast.File{file})
	want := map[string]bool{"Text": true, "Panel": true, "Promoted": true, "Deep": true, "ByValue": true, "ViaUnexported": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("component types = %v, want %v", got, want)
	}
}

// TestEveryExportedComponentTypeIsInTheCacheContract keeps the walker complete: an exported type of this package that is a component (see
// contractComponentTypes) must be built by contractFixtures or exempted with its reason.
func TestEveryExportedComponentTypeIsInTheCacheContract(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			file, err := parser.ParseFile(fset, name, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, file)
		}
	}
	components := contractComponentTypes(files)
	fixtures := contractFixtures()
	built := map[string]bool{}
	for name := range fixtures {
		typeName, _, _ := strings.Cut(name, "#") // "Type#variant" is another fixture of Type
		built[typeName] = true
	}
	for name := range components {
		reason, exempt := contractExempt[name]
		switch {
		case built[name] && exempt:
			t.Errorf("%s is both built by contractFixtures and exempted (%s)", name, reason)
		case !built[name] && !exempt:
			t.Errorf("exported component type %s is not in the cache contract: add a fixture to contractFixtures, or name it in contractExempt with the reason it cannot show a stale child", name)
		}
	}
	for name := range built {
		if !components[name] {
			t.Errorf("contractFixtures builds %s, which is not an exported component type of this package", name)
		}
	}
	for name := range contractExempt {
		if !components[name] {
			t.Errorf("contractExempt names %s, which is not an exported component type of this package", name)
		}
	}
}

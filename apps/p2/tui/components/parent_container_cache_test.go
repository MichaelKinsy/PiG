package components

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// Pi's Container renders every child on every frame. A Go parent container caches the lines of a clean child, so what it shows for a component after a mutation must be what
// the component renders by itself: these are the components of this package whose mutators change a Container beneath them, which the component's embedded Container
// reports to every ancestor (tui.Container.IsDirty).
func requireParentSeesTheChange(t *testing.T, child pitui.Component, change func()) {
	t.Helper()
	parent := pitui.NewContainer(child)
	_ = parent.Render(80)
	change()
	got, want := slices.Clone(parent.Render(80)), child.Render(80)
	if !slices.Equal(got, want) {
		t.Fatalf("parent render:\n%s\ndirect render:\n%s", plain(got), plain(want))
	}
}

func TestBashExecutionOutputReachesAParentContainersRender(t *testing.T) {
	c := NewBashExecutionComponent("echo hi", newTestContext(t), BashExecutionOptions{})
	defer c.Dispose()
	requireParentSeesTheChange(t, c, func() { c.SetOutput("hello\nworld", false) })
}

func TestBashExecutionCompletionReachesAParentContainersRender(t *testing.T) {
	c := NewBashExecutionComponent("false", newTestContext(t), BashExecutionOptions{})
	defer c.Dispose()
	code := 2.0
	requireParentSeesTheChange(t, c, func() { c.SetComplete(&code, false, BashCompletion{}) })
}

func TestBashExecutionExpandReachesAParentContainersRender(t *testing.T) {
	c := NewBashExecutionComponent("echo hi", newTestContext(t), BashExecutionOptions{})
	defer c.Dispose()
	var out []string
	for i := range 30 {
		out = append(out, "line-"+string(rune('A'+i%26)))
	}
	c.SetOutput(strings.Join(out, "\n"), false)
	requireParentSeesTheChange(t, c, func() { c.SetExpanded(true) })
}

func TestSkillUserMessageExpandReachesAParentContainersRender(t *testing.T) {
	ctx := newTestContext(t)
	item := transcript.UserItem{Key: "u", Text: "<skill name=\"deploy\" location=\"/s/deploy.md\">\nStep one\nStep two\n</skill>\n\nplease go"}
	c := NewUserMessageComponent(item, ctx)
	requireParentSeesTheChange(t, c, func() { c.SetExpanded(true) })
}

func TestSelectorKeyReachesAParentContainersRender(t *testing.T) {
	ctx := newOracleContext(t, "dark", pitui.TerminalColorModeTrueColor)
	c := NewModelSelector(ctx, ModelSelectorOptions{Models: catalogModels(), OnSelect: func(SelectorModel) {}, OnCancel: func() {}})
	requireParentSeesTheChange(t, c, func() { c.HandleInput(keyDownArrow) })
}

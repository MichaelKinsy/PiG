package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestSteppedSubmenuInitialContextCompletionAndBack(t *testing.T) {
	initial := map[string]string{"model": "one"}
	steps := []SteppedSubmenuStep{
		{Key: "model", Title: func(map[string]string) string { return "Models" }, Description: func(map[string]string) string { return "Choose" }, Options: func(map[string]string) []tui.SelectItem { return []tui.SelectItem{{Value: "one", Label: "One"}} }},
		{Key: "level", Title: func(ctx map[string]string) string { return ctx["model"] }, Description: func(map[string]string) string { return "Level" }, Options: func(map[string]string) []tui.SelectItem { return []tui.SelectItem{{Value: "high", Label: "High"}} }},
	}
	var got map[string]string
	cancelled := 0
	menu := NewSteppedSubmenu(steps, func(values map[string]string) { got = values }, func() { cancelled++ }, SteppedSubmenuOptions{StartAtStep: 1, InitialContext: initial})
	initial["model"] = "external mutation"
	menu.HandleInput("\r")
	if got["model"] != "one" || got["level"] != "high" || cancelled != 1 {
		t.Fatalf("completion = %v, cancels %d", got, cancelled)
	}
	got["level"] = "external mutation"
	if menu.context["level"] != "high" {
		t.Fatal("completion context aliases submenu state")
	}

	menu = NewSteppedSubmenu(steps, func(map[string]string) { t.Fatal("back completed") }, func() { cancelled++ }, SteppedSubmenuOptions{StartAtStep: 1, InitialContext: map[string]string{"model": "one", "level": "high"}})
	menu.HandleInput("\x1b")
	if menu.stepIndex != 0 || menu.context["level"] != "" || menu.context["model"] != "one" {
		t.Fatalf("back context = %v, step %d", menu.context, menu.stepIndex)
	}
}

// settings-submenu.ts SteppedSubmenu.buildStep: Esc on a later step rebuilds the previous step (not the first), deleting
// only the current step's key; with loop, completing the last step delivers a copy and restarts at step 0 with an
// empty context. The step label counts every step.
func TestSteppedSubmenuThreeStepBackAndLoop(t *testing.T) {
	step := func(key string) SteppedSubmenuStep {
		return SteppedSubmenuStep{Key: key, Title: func(map[string]string) string { return key }, Description: func(map[string]string) string { return "pick " + key }, Options: func(map[string]string) []tui.SelectItem { return []tui.SelectItem{{Value: key + "-value", Label: key}} }}
	}
	var completed []map[string]string
	cancelled := 0
	menu := NewSteppedSubmenu([]SteppedSubmenuStep{step("a"), step("b"), step("c")}, func(values map[string]string) { completed = append(completed, values) }, func() { cancelled++ }, SteppedSubmenuOptions{Loop: true})
	menu.HandleInput("\r")
	menu.HandleInput("\r")
	if menu.stepIndex != 2 {
		t.Fatalf("step = %d after two selections", menu.stepIndex)
	}
	menu.HandleInput("\x1b")
	if menu.stepIndex != 1 || menu.context["a"] != "a-value" || menu.context["b"] != "b-value" {
		t.Fatalf("Esc on step 3: step %d, context %v", menu.stepIndex, menu.context)
	}
	if text := stripANSITest(strings.Join(menu.Render(80), "\n")); !strings.Contains(text, "Step 2/3 · pick b") {
		t.Fatalf("step label:\n%s", text)
	}
	menu.HandleInput("\r")
	menu.HandleInput("\r")
	if len(completed) != 1 || completed[0]["c"] != "c-value" || cancelled != 0 {
		t.Fatalf("completion = %v, cancels %d", completed, cancelled)
	}
	if menu.stepIndex != 0 || len(menu.context) != 0 {
		t.Fatalf("loop restarted at step %d with %v", menu.stepIndex, menu.context)
	}
}

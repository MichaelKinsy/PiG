package codingagent

import (
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

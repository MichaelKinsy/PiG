package planmode

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func BenchmarkPlanModeUtilities(b *testing.B) {
	b.Run("ordinary-command", func(b *testing.B) {
		for b.Loop() {
			if !IsSafeCommand("git status --short") {
				b.Fatal("read-only command rejected")
			}
		}
	})
	b.Run("long-blocked-command", func(b *testing.B) {
		command := "echo " + strings.Repeat("ordinary input ", 4096) + "; npm\u00a0install package"
		b.SetBytes(int64(len(command)))
		for b.Loop() {
			if IsSafeCommand(command) {
				b.Fatal("destructive command accepted")
			}
		}
	})
	const steps = 512
	var plan, markers strings.Builder
	plan.WriteString("Plan:\n")
	for i := 1; i <= steps; i++ {
		fmt.Fprintf(&plan, "%d. Inspect the implementation of subsystem %d\n", i, i)
		fmt.Fprintf(&markers, "[DONE:%d]\n", i)
	}
	planText, markerText := plan.String(), markers.String()
	b.Run("extract-plan", func(b *testing.B) {
		b.SetBytes(int64(len(planText)))
		for b.Loop() {
			if got := ExtractTodoItems(planText); len(got) != steps {
				b.Fatalf("extracted %d of %d source steps", len(got), steps)
			}
		}
	})
	b.Run("complete-plan", func(b *testing.B) {
		items := ExtractTodoItems(planText)
		b.SetBytes(int64(len(markerText)))
		for b.Loop() {
			current := slices.Clone(items)
			if count := MarkCompletedSteps(markerText, current); count != steps || !allTodosCompleted(current) {
				b.Fatal("plan completion lost markers")
			}
		}
	})
}

package harness

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Pi 32c71a6d4 tags Wrap with kind: "tool" | "section" and GenerationHooks.onYield results with kind: "continue" | "tasks".
func TestWrapAndYieldResultExposeKindTags(t *testing.T) {
	wrapIface := reflect.TypeFor[durable.Wrap]()
	if _, ok := wrapIface.MethodByName("WrapKind"); !ok {
		t.Fatalf("durable.Wrap has no WrapKind() method; want kind tag tool | section")
	}
	yieldIface := reflect.TypeFor[YieldResult]()
	if _, ok := yieldIface.MethodByName("YieldKind"); !ok {
		t.Fatalf("harness.YieldResult has no YieldKind() method; want kind tag continue | tasks")
	}
	tw := WrapTool(registryTool("bash"), func(inner *durable.ToolRegistration) *durable.ToolRegistration { return inner })
	sw := WrapSection("cwd", func(inner *durable.PromptSection) *durable.PromptSection { return inner })
	if tw.WrapKind() != durable.WrapKindTool || string(tw.WrapKind()) != "tool" {
		t.Fatalf("WrapTool.WrapKind() = %q, want tool", tw.WrapKind())
	}
	if sw.WrapKind() != durable.WrapKindSection || string(sw.WrapKind()) != "section" {
		t.Fatalf("WrapSection.WrapKind() = %q, want section", sw.WrapKind())
	}
	var yc YieldResult = &YieldContinue{}
	var yt YieldResult = &YieldTasks{}
	if yc.YieldKind() != YieldResultContinue || string(yc.YieldKind()) != "continue" {
		t.Fatalf("YieldContinue.YieldKind() = %q, want continue", yc.YieldKind())
	}
	if yt.YieldKind() != YieldResultTasks || string(yt.YieldKind()) != "tasks" {
		t.Fatalf("YieldTasks.YieldKind() = %q, want tasks", yt.YieldKind())
	}
}

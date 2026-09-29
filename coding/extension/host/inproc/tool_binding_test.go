package inproc_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func TestBindToolsPreservesModeActions(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/core/agent-session.ts:3063-3112 binds live tool getters alongside the existing mode context.
	runner := inproc.NewRunner(nil, t.TempDir())
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{IsProjectTrusted: func() bool { return false }}, nil)
	infos := []extension.ToolInfo{{Name: "custom", PromptGuidelines: []string{"Use custom."}}}
	active := []string{"custom"}
	var sent any
	var delivery extension.DeliverAs
	runner.BindTools(extension.ContextActions{GetAllTools: func() []extension.ToolInfo { return infos }, GetActiveTools: func() []string { return active }, SetActiveTools: func(names []string) { active = slices.Clone(names) }, GetSystemPrompt: func() string { return "tool prompt" }, SendUserMessage: func(content any, opts *extension.SendUserMessageOptions) error {
		sent, delivery = content, opts.DeliverAs
		return nil
	}})
	ctx := extension.FromContext(runner.DispatchContext(t.Context()))
	if err := ctx.SendUserMessage("bound message", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsSteer}); err != nil {
		t.Fatal(err)
	}
	if sent != "bound message" || delivery != extension.DeliverAsSteer {
		t.Fatalf("bound message=%v delivery=%q", sent, delivery)
	}
	if !reflect.DeepEqual(ctx.GetAllTools(), infos) || !slices.Equal(ctx.GetActiveTools(), active) {
		t.Fatal("bound tool views differ")
	}
	ctx.SetActiveTools([]string{})
	if len(ctx.GetActiveTools()) != 0 {
		t.Fatal("active tools did not change")
	}
	prompt, err := ctx.GetSystemPrompt()
	if err != nil || prompt != "tool prompt" {
		t.Fatalf("prompt %q, %v", prompt, err)
	}
	trusted, err := ctx.IsProjectTrusted()
	if err != nil || trusted {
		t.Fatalf("mode-owned trust action changed: %v, %v", trusted, err)
	}
	runner.Invalidate("closed")
	if _, err := ctx.GetSystemPrompt(); err == nil {
		t.Fatal("stale prompt action remained callable")
	}
	if err := ctx.SendUserMessage("stale message", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsFollowUp}); err == nil {
		t.Fatal("stale message action remained callable")
	}
	if sent != "bound message" {
		t.Fatal("stale context invoked message action")
	}
}

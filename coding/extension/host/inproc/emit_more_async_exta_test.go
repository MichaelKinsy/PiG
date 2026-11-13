package inproc

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// extWith is an extension whose one handler of event is fn.
func extWith(path, event string, fn extension.HandlerFn) extension.Extension {
	return extension.Extension{Path: path, Handlers: map[string][]extension.HandlerFn{event: {fn}}}
}

// runner.ts emitContext (1312, 1348): context_with_system handlers run in turn, each sees its predecessor's messages, and a handler that
// throws is reported without ending the chain.
func TestRunnerContextWithSystemChainsMessagesPastAFailingHandler(t *testing.T) {
	appendMessage := func(text string, seen *[]int) extension.HandlerFn {
		return func(args ...any) (any, error) {
			messages := args[0].(extension.ContextWithSystemEvent).Messages
			*seen = append(*seen, len(messages))
			return &extension.ContextEventResult{Messages: append(append([]extension.AgentMessage(nil), messages...), customMessage(text))}, nil
		}
	}
	var seen []int
	exts := []extension.Extension{
		extWith("/ext/a.ts", "context_with_system", appendMessage("a", &seen)),
		extWith("/ext/b.ts", "context_with_system", func(...any) (any, error) { return nil, errors.New("handler failed") }),
		extWith("/ext/c.ts", "context_with_system", appendMessage("c", &seen)),
	}
	r := NewRunner(exts, t.TempDir())
	var reported []string
	r.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.ExtensionPath+": "+err.Error) })
	got, err := r.EmitContextWithSystem(context.Background(), []extension.AgentMessage{customMessage("original")})
	want := []extension.AgentMessage{customMessage("original"), customMessage("a"), customMessage("c")}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %v, %v; want %v", got, err, want)
	}
	if !reflect.DeepEqual(seen, []int{1, 2}) || !reflect.DeepEqual(reported, []string{"/ext/b.ts: handler failed"}) {
		t.Fatalf("handlers saw %v message counts, reported %q", seen, reported)
	}
}

// runner.ts emitBeforeAgentStart (1434-1470): the custom messages of the handlers are collected in handler order and a handler that throws
// is reported without ending the chain.
func TestRunnerBeforeAgentStartCollectsMessagesInOrderPastAFailingHandler(t *testing.T) {
	message := func(text string) extension.HandlerFn {
		return func(...any) (any, error) {
			return &extension.BeforeAgentStartEventResult{Message: &extension.CustomMessageRef{CustomType: text, Content: text}}, nil
		}
	}
	exts := []extension.Extension{
		extWith("/ext/a.ts", "before_agent_start", message("a")),
		extWith("/ext/b.ts", "before_agent_start", func(...any) (any, error) { return nil, errors.New("handler failed") }),
		extWith("/ext/c.ts", "before_agent_start", message("c")),
	}
	r := NewRunner(exts, t.TempDir())
	r.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	var reported []string
	r.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.ExtensionPath+": "+err.Error) })
	result, err := r.EmitBeforeAgentStart(context.Background(), extension.UserPromptTrigger("hello", nil), extension.BuildSystemPromptOptions{Cwd: t.TempDir()})
	if err != nil || result == nil {
		t.Fatalf("result = %+v, %v", result, err)
	}
	var types []string
	for _, m := range result.Messages {
		types = append(types, m.CustomType)
	}
	if !reflect.DeepEqual(types, []string{"a", "c"}) || !reflect.DeepEqual(reported, []string{"/ext/b.ts: handler failed"}) {
		t.Fatalf("messages = %q, reported %q", types, reported)
	}
}

// runner.ts emitResourcesDiscover (1486-1530): each handler is awaited in turn, its paths are attributed to its extension in handler order,
// and a handler that throws is reported without ending the chain.
func TestRunnerResourcesDiscoverAggregatesInOrderPastAFailingHandler(t *testing.T) {
	paths := func(skill, prompt string) extension.HandlerFn {
		return func(...any) (any, error) {
			return &extension.ResourcesDiscoverResult{SkillPaths: []string{skill}, PromptPaths: []string{prompt}}, nil
		}
	}
	exts := []extension.Extension{
		extWith("/ext/a.ts", "resources_discover", paths("skill-a", "prompt-a")),
		extWith("/ext/b.ts", "resources_discover", func(...any) (any, error) { return nil, errors.New("handler failed") }),
		extWith("/ext/c.ts", "resources_discover", paths("skill-c", "prompt-c")),
	}
	r := NewRunner(exts, t.TempDir())
	var reported []string
	r.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.ExtensionPath+": "+err.Error) })
	got, err := r.EmitResourcesDiscover(context.Background(), "/cwd", "startup")
	if err != nil || got == nil {
		t.Fatalf("result = %+v, %v", got, err)
	}
	wantSkills := []extension.AttributedResourcePath{{Path: "skill-a", ExtensionPath: "/ext/a.ts"}, {Path: "skill-c", ExtensionPath: "/ext/c.ts"}}
	wantPrompts := []extension.AttributedResourcePath{{Path: "prompt-a", ExtensionPath: "/ext/a.ts"}, {Path: "prompt-c", ExtensionPath: "/ext/c.ts"}}
	if !reflect.DeepEqual(got.SkillPaths, wantSkills) || !reflect.DeepEqual(got.PromptPaths, wantPrompts) {
		t.Fatalf("skills %v prompts %v", got.SkillPaths, got.PromptPaths)
	}
	if !reflect.DeepEqual(reported, []string{"/ext/b.ts: handler failed"}) {
		t.Fatalf("reported = %q", reported)
	}
}

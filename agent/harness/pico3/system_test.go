package pico3

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func systemEntries(env *testEnv, conversation ...Id) []Entry {
	var rows []Entry
	for _, entry := range env.entries(conversation...) {
		if entry.Kind == "pi.system" {
			rows = append(rows, entry)
		}
	}
	return rows
}

func TestSystemSectionsBaselineDeltaAndHeadOmissions(t *testing.T) {
	prompt := "P1"
	env := openEnv(t, openOptions{hooks: &hooksByKind{generation: &GenerationHooks{SystemInstructions: func(_ context.Context, input SystemInstructionsInput, _ HookApi) (*SystemInstructionsResult, error) {
		input.Sections.Set(SystemSections.Identity, prompt)
		return nil, nil
	}}}, root: &RootSpec{Rewindable: JsonObject{"model": testModel, "keepRecent": 10}}})
	env.wait(env.send(env.root, "one"))
	env.wait(env.send(env.root, "two"))
	equal(t, len(systemEntries(env)), 1, "unchanged sections")
	prompt = "P2"
	env.wait(env.send(env.root, "three"))
	entries := systemEntries(env)
	equal(t, len(entries), 2, "changed sections")
	equal(t, entries[0].Data["baseline"], true, "initial baseline")
	if _, ok := entries[1].Data["baseline"]; ok {
		t.Fatal("delta marked baseline")
	}
	records := arr(entries[1].Data, "sections")
	equal(t, len(records), 1, "one delta")
	equal(t, str(asObject(records[0]), "key"), "identity", "delta key")
	equal(t, str(asObject(records[0]), "action"), "set", "delta action")
	if !strings.Contains(str(entries[1].Model[0], "content"), "identity section now reads:\nP2") {
		t.Fatal("missing delta text")
	}
	check(t, env.root.Reset(bg, nil))
	env.wait(env.send(env.root, "four"))
	entries = systemEntries(env)
	equal(t, len(entries), 3, "post-head baseline")
	equal(t, entries[2].Data["baseline"], true, "baseline")
	if !strings.Contains(str(entries[2].Model[0], "content"), "## identity\nP2") {
		t.Fatal("missing full baseline")
	}
	count := 0
	for _, message := range env.context(1).Messages {
		if message["role"] == "system" {
			count++
		}
	}
	equal(t, count, 1, "prior managed entries omitted")
}

func TestSystemDraftWrapDeleteAndThrowRollback(t *testing.T) {
	custom := DefineSystemSection("custom", func(value JsonValue) string { return fmt.Sprintf("custom=%g", numberOr(asObject(value)["n"], 0)) })
	throwOnce := true
	env := openEnv(t, openOptions{sections: []*SystemSection{custom}, hooks: &hooksByKind{generation: &GenerationHooks{SystemInstructions: func(_ context.Context, input SystemInstructionsInput, _ HookApi) (*SystemInstructionsResult, error) {
		input.Sections.Set(SystemSections.Identity, "I am")
		input.Sections.Set(custom, JsonObject{"n": 1})
		input.Sections.Wrap(SystemSections.Identity, func(text string) string { return "[" + text + "]" })
		return nil, nil
	}}}})
	namespace := must(env.h.Namespace("section.hooks", NamespaceDefaults{}, nil))
	must(env.h.Hooks(namespace, Kinds.Generation, &GenerationHooks{SystemInstructions: func(_ context.Context, input SystemInstructionsInput, _ HookApi) (*SystemInstructionsResult, error) {
		if throwOnce {
			throwOnce = false
			input.Sections.Set(custom, JsonObject{"n": 999})
			return nil, errors.New("boom")
		}
		return nil, nil
	}}))
	env.wait(env.send(env.root, "one"))
	entries := systemEntries(env)
	text := str(entries[0].Model[0], "content")
	if !strings.Contains(text, "## identity\n[I am]") || !strings.Contains(text, "## custom\ncustom=1") {
		t.Fatalf("draft rollback/wrap: %s", text)
	}
	var keys []string
	for _, record := range arr(entries[0].Data, "sections") {
		keys = append(keys, str(asObject(record), "key"))
	}
	equal(t, keys, []string{"identity", "custom"}, "section order")
	must(env.h.Hooks(namespace, Kinds.Generation, &GenerationHooks{SystemInstructions: func(_ context.Context, input SystemInstructionsInput, _ HookApi) (*SystemInstructionsResult, error) {
		input.Sections.Delete(custom.Key)
		return nil, nil
	}}))
	env.wait(env.send(env.root, "two"))
	entries = systemEntries(env)
	equal(t, len(entries), 2, "remove delta")
	equal(t, arr(entries[1].Data, "sections"), []any{JsonObject{"key": "custom", "action": "remove"}}, "remove record")
	if !strings.Contains(str(entries[1].Model[0], "content"), "custom section no longer applies") {
		t.Fatal("missing remove text")
	}
}

func TestSystemPreparationRetriesChangedConfig(t *testing.T) {
	gate := &testGate{}
	runs := 0
	env := openEnv(t, openOptions{hooks: &hooksByKind{generation: &GenerationHooks{SystemInstructions: func(ctx context.Context, input SystemInstructionsInput, _ HookApi) (*SystemInstructionsResult, error) {
		runs++
		input.Sections.Set(SystemSections.Identity, fmt.Sprint("profile=", input.Config.Profile))
		if runs == 1 {
			return nil, gate.Wait(ctx)
		}
		return nil, nil
	}}}})
	input := env.send(env.root, "one")
	gate.Arrivals(t, 1)
	check(t, env.root.Config().Set(bg, JsonObject{"profile": "changed"}))
	gate.Open()
	env.wait(input)
	equal(t, runs, 2, "stale preparation rerun")
	entries := systemEntries(env)
	if !strings.Contains(str(entries[0].Model[0], "content"), "profile=changed") {
		t.Fatal("stale system entry")
	}
}

func TestNewConversationAppliesSectionSeed(t *testing.T) {
	env := openEnv(t, openOptions{})
	child := must(env.h.CreateConversation(bg, ConversationSpec{Rewindable: JsonObject{"model": testModel}, Sections: []SectionSeed{SectionSeedOf(SystemSections.Identity, "seeded identity")}}, nil))
	env.wait(env.send(child, "hi"))
	entries := systemEntries(env, child.Id)
	if !strings.Contains(str(entries[0].Model[0], "content"), "## identity\nseeded identity") {
		t.Fatal("missing seed")
	}
}

func TestConfigurationRoutingResetAndCollision(t *testing.T) {
	plan := quickKind(t, "plan", func(Task) JsonValue { return nil })
	plan.Config = &KindConfig{Rewindable: []ConfigKey{{Key: "planMode", Default: false}}}
	env := openEnv(t, openOptions{taskKinds: []*Kind{plan}})
	before := must(env.root.Config().Get(bg))
	equal(t, before["planMode"], false, "custom default")
	equal(t, before["followUpMode"], "one-at-a-time", "sticky default")
	check(t, env.root.Config().Set(bg, JsonObject{"planMode": true, "followUpMode": "all", "keepRecent": 5}))
	rw := must(env.root.Rewindable(bg))
	equal(t, rw["planMode"], true, "custom route")
	equal(t, rw["keepRecent"], 5, "core route")
	equal(t, env.sticky()["followUpMode"], "all", "sticky route")
	check(t, env.root.Config().Reset(bg, []string{"planMode", "followUpMode", "keepRecent"}))
	reset := must(env.root.Config().Get(bg))
	equal(t, reset["planMode"], false, "reset custom")
	equal(t, reset["followUpMode"], "one-at-a-time", "reset sticky")
	equal(t, reset["keepRecent"], 20000, "reset core")
	clash := quickKind(t, "clash", func(Task) JsonValue { return nil })
	clash.Config = &KindConfig{Sticky: []ConfigKey{{Key: "model", Default: ""}}}
	if _, err := env.h.RegisterTaskKind(clash); err == nil || !strings.Contains(err.Error(), "declared by more than one kind") {
		t.Fatalf("config collision: %v", err)
	}
}

func TestPluginHandlerResultAndMissingHandler(t *testing.T) {
	env := openEnv(t, openOptions{plugins: map[string]PluginHandler{"double": func(_ context.Context, input JsonValue, _ *ToolApi) (JsonValue, error) {
		return numberOr(input, 0) * 2, nil
	}}})
	ref := createTestTask(t, env, Kinds.Plugin, JsonObject{"handler": "double", "input": 21})
	equal(t, env.untilTerminal(ref.Id).Outcome, &Outcome{Status: OutcomeCompleted, Result: float64(42)}, "result")
	ref = createTestTask(t, env, Kinds.Plugin, JsonObject{"handler": "missing", "input": nil})
	equal(t, str(asObject(env.untilTerminal(ref.Id).Outcome.Failure), "reason"), "missing_handler", "missing handler")
}

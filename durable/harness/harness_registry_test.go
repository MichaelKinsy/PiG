// Ports packages/durable/test/harness-registry.test.ts.

package harness

// pi: packages/durable/src/harness/registry.ts

// pi: packages/durable/src/harness/define.ts

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// registryTool builds a tool. Upstream's AppTool adds an application field (snippet); a Go registration carries such data in its own fields, so the snippet rides in PromptGuidelines here.
func registryTool(name string, edit ...func(*durable.ToolRegistration)) *durable.ToolRegistration {
	tool := &durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: name, Description: name + " tool", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
		},
	}
	for _, apply := range edit {
		apply(tool)
	}
	return tool
}

type registryTaskState struct {
	Phase string `json:"phase"`
}

func registryTask(name string, version ...int) durable.Task[durable.JsonValue, registryTaskState, durable.JsonValue, any] {
	v := 1
	if len(version) > 0 {
		v = version[0]
	}
	noop := func(context.Context, durable.RunningTask[durable.JsonValue, registryTaskState, durable.JsonValue], durable.TaskRuntime[durable.JsonValue, registryTaskState, durable.JsonValue, any]) error {
		return nil
	}
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, registryTaskState, durable.JsonValue, any]{
		Name:    name,
		Version: v,
		Initial: func(durable.JsonValue) registryTaskState { return registryTaskState{Phase: "run"} },
		Phases:  map[string]durable.PhaseHandler[durable.JsonValue, registryTaskState, durable.JsonValue, any]{"run": noop},
		Abort:   noop,
	})
}

func extensionNamesOf(extensions []*durable.Extension) []string {
	names := []string{}
	for _, extension := range extensions {
		names = append(names, extension.Name)
	}
	return names
}

func toolNamesOf(tools []*durable.ToolRegistration) []string {
	names := []string{}
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func text(value string) func(context.Context, durable.PromptInput) (*string, error) {
	return func(context.Context, durable.PromptInput) (*string, error) { return new(value), nil }
}

func resolveWith(state *AgentState, snapshot durable.RegistrySnapshot, settings *HarnessSettings, reports *[]error) durable.Agent {
	return ResolveAgent(state, snapshot, ResolveSettings(settings), func(err error) {
		if reports != nil {
			*reports = append(*reports, err)
		}
	})
}

type renderedSection struct {
	key  string
	text *string
}

func renderedOf(t *testing.T, agent durable.Agent) []renderedSection {
	t.Helper()
	input := durable.PromptInput{ConversationId: 1, Agent: agent, Shown: map[string]string{}, Read: emptyReader{}}
	var out []renderedSection
	for _, section := range agent.Sections {
		rendered, err := section.Render(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, renderedSection{key: section.Key, text: rendered})
	}
	return out
}

// emptyReader is upstream's `{ snapshot: async () => undefined, snapshotAsOf: async () => undefined }`.
type emptyReader struct{}

func (emptyReader) SnapshotErased(context.Context, durable.AnyDocToken, ...any) (durable.JsonObject, error) {
	return nil, nil
}

func (emptyReader) SnapshotAsOfErased(context.Context, durable.AnyDocToken, durable.EntryId, ...any) (durable.JsonObject, error) {
	return nil, nil
}

func mustInstall(t *testing.T, registry Registry, extensions ...*durable.Extension) {
	t.Helper()
	for _, extension := range extensions {
		if err := registry.Install(extension); err != nil {
			t.Fatal(err)
		}
	}
}

func expectStrings(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func sameTask(a, b durable.AnyTask) bool {
	return a != nil && b != nil && a.AnyDefinition() == b.AnyDefinition()
}

// upstream: packages/durable/test/harness-registry.test.ts "keeps the default of a progress interval given as undefined" (1.0.4).
// A nil field of the patch is JavaScript's undefined: it leaves the built-in default, whichever the sibling field says.
func TestSettingsKeepTheDefaultOfAProgressIntervalGivenAsUndefined(t *testing.T) {
	progress := ResolveSettings(&HarnessSettings{Progress: &ProgressPolicyPatch{PartialIntervalMs: nil, OutputIntervalMs: new(250.0)}}).Progress
	if progress.PartialIntervalMs != 100 || progress.OutputIntervalMs != 250 {
		t.Fatalf("progress = %+v, want partialIntervalMs 100 and outputIntervalMs 250", progress)
	}
	if got := ResolveSettings(&HarnessSettings{Progress: &ProgressPolicyPatch{}}).Progress; got != DefaultProgressPolicy {
		t.Fatalf("an empty patch resolved to %+v, want the defaults %+v", got, DefaultProgressPolicy)
	}
}

// Pi source: packages/durable/src/harness/registry.ts
// mutation-checked: zeroing the results of Registry.Install fails it
// packages/durable/src/harness/types.ts:298-303: Registry.install(extension) installs or replaces by name in place and publishes at once; Registry.uninstall(extension) removes by name whichever object it is, and a later install appends.
func TestRegistry(t *testing.T) {
	t.Run("installs, replaces in place, and uninstalls extensions by name", func(t *testing.T) {
		registry := CreateRegistry()
		var listener []string
		registry.Subscribe(func() {
			listener = append(listener, strings.Join(extensionNamesOf(registry.Snapshot().Installed()), ","))
		})
		a := new(durable.Extension{Name: "a", Tools: []*durable.ToolRegistration{registryTool("read", func(tool *durable.ToolRegistration) {
			tool.PromptGuidelines = []string{"Read files"}
		})}})
		b := new(durable.Extension{Name: "b", Tools: []*durable.ToolRegistration{registryTool("read"), registryTool("bash")}})
		mustInstall(t, registry, a, b)
		before := registry.Snapshot()
		// A new object with an installed name replaces it at its position.
		a2 := new(durable.Extension{Name: "a", Tools: []*durable.ToolRegistration{registryTool("grep")}})
		mustInstall(t, registry, a2)
		expectStrings(t, extensionNamesOf(registry.Snapshot().Installed()), []string{"a", "b"})
		if registry.Snapshot().Extension("a") != a2 {
			t.Fatal("a is not replaced by a2")
		}
		var placed []string
		for _, entry := range registry.Snapshot().Tools() {
			placed = append(placed, entry.Tool.Name+"@"+entry.Extension.Name)
		}
		expectStrings(t, placed, []string{"grep@a", "read@b", "bash@b"})
		// Old snapshots stay as they were.
		if before.Extension("a") != a {
			t.Fatal("old snapshot changed")
		}
		expectStrings(t, before.Tools()[0].Tool.PromptGuidelines, []string{"Read files"})
		// Uninstall matches the name, whichever object; a later install appends.
		registry.Uninstall(a)
		registry.Uninstall(a)
		expectStrings(t, extensionNamesOf(registry.Snapshot().Installed()), []string{"b"})
		mustInstall(t, registry, a)
		expectStrings(t, extensionNamesOf(registry.Snapshot().Installed()), []string{"b", "a"})
		expectStrings(t, listener, []string{"a", "a,b", "a,b", "b", "b,a"})
	})

	t.Run("validates the registry as it would be after an install and publishes nothing when invalid", func(t *testing.T) {
		registry := CreateRegistry()
		tasks := new(durable.Extension{Name: "tasks", Tasks: []durable.AnyTask{registryTask("app.index")}})
		mustInstall(t, registry, tasks)
		published := 0
		registry.Subscribe(func() { published++ })
		before := registry.Snapshot()
		invalid := []struct {
			extension *durable.Extension
			message   string
		}{
			{new(durable.Extension{Name: "x", Tools: []*durable.ToolRegistration{registryTool("read"), registryTool("read")}}), "two tools named read"},
			{new(durable.Extension{Name: "x", Sections: []*durable.PromptSection{Section("a", text("1")), Section("a", text("2"))}}), "two sections"},
			{new(durable.Extension{Name: "x", Sections: []*durable.PromptSection{Section("Bad Key", text(""))}}), "must match"},
			{new(durable.Extension{Name: "x", Sections: []*durable.PromptSection{Section("instructions", text(""))}}), "reserved"},
			{new(durable.Extension{Name: "x", Tasks: []durable.AnyTask{registryTask("pi.generation")}}), "already installed"},
			{new(durable.Extension{Name: "x", Tasks: []durable.AnyTask{registryTask("app.index")}}), "already installed"},
		}
		for _, item := range invalid {
			err := registry.Install(item.extension)
			if err == nil || !strings.Contains(err.Error(), item.message) {
				t.Fatalf("install error %v, want %q", err, item.message)
			}
		}
		if registry.Snapshot() != before {
			t.Fatal("an invalid install published")
		}
		if published != 0 {
			t.Fatalf("published %d", published)
		}
		// Replacing the extension that holds a task name is valid: the check runs on the state after replacement.
		mustInstall(t, registry, new(durable.Extension{Name: "tasks", Tasks: []durable.AnyTask{registryTask("app.index", 2)}}))
		if got := registry.Snapshot().Task("app.index").AnyDefinition().Version; got != 2 {
			t.Fatalf("version %d", got)
		}
	})

	t.Run("always holds the built-in tasks, which are not an extension", func(t *testing.T) {
		registry := CreateRegistry()
		if len(registry.Snapshot().Installed()) != 0 {
			t.Fatal("installed is not empty")
		}
		expectTasks := func(want ...durable.AnyTask) {
			t.Helper()
			got := registry.Snapshot().Tasks()
			if len(got) != len(want) {
				t.Fatalf("tasks %d, want %d", len(got), len(want))
			}
			for i := range got {
				if !sameTask(got[i], want[i]) {
					t.Fatalf("task %d is %s, want %s", i, got[i].AnyDefinition().Name, want[i].AnyDefinition().Name)
				}
			}
		}
		expectTasks(GenerationTask, ToolTask, CompactionTask)
		custom := registryTask("app.custom")
		mustInstall(t, registry, new(durable.Extension{Name: "custom", Tasks: []durable.AnyTask{custom}}))
		expectTasks(GenerationTask, ToolTask, CompactionTask, custom)
		if !sameTask(registry.Snapshot().Task("app.custom"), custom) {
			t.Fatal("app.custom is not the installed task")
		}
		registry.Uninstall(new(durable.Extension{Name: "custom"}))
		if registry.Snapshot().Task("app.custom") != nil {
			t.Fatal("app.custom survived uninstall")
		}
	})
}

func TestAgentResolution(t *testing.T) {
	read := registryTool("read")
	bash := registryTool("bash")
	edit := registryTool("edit")
	coding := new(durable.Extension{
		Name:     "coding",
		Tools:    []*durable.ToolRegistration{read, bash, edit},
		Sections: []*durable.PromptSection{Section("preamble", text("You code."), SectionOptions{Tag: new(false)}), Section("cwd", text("/repo"))},
	})
	skills := new(durable.Extension{Name: "skills", Sections: []*durable.PromptSection{Section("skills", text("S"))}})
	reviewer := new(durable.Extension{Name: "reviewer", Sections: []*durable.PromptSection{Section("role", text("Review."))}})
	registry := CreateRegistry()
	mustInstall(t, registry, coding, skills, reviewer)
	snapshot := registry.Snapshot()

	t.Run("selects the default, an array, or the default edited by add and remove", func(t *testing.T) {
		expectStrings(t, extensionNamesOf(resolveWith(nil, snapshot, nil, nil).Extensions), []string{"coding", "skills", "reviewer"})
		settings := &HarnessSettings{Extensions: []*durable.Extension{coding, skills}}
		expectStrings(t, extensionNamesOf(resolveWith(&AgentState{}, snapshot, settings, nil).Extensions), []string{"coding", "skills"})
		exact := &AgentState{Extensions: &ExtensionSelection{Exact: true, Names: []string{"reviewer", "coding"}}}
		expectStrings(t, extensionNamesOf(resolveWith(exact, snapshot, settings, nil).Extensions), []string{"reviewer", "coding"})
		// Add appends, remove drops, duplicates keep their first position, uninstalled names are skipped.
		edited := &AgentState{Extensions: &ExtensionSelection{Add: []string{"reviewer", "coding", "gone"}, Remove: []string{"skills"}}}
		expectStrings(t, extensionNamesOf(resolveWith(edited, snapshot, settings, nil).Extensions), []string{"coding", "reviewer"})
		// An old object stands for its name: the installed extension is selected.
		stale := &HarnessSettings{Extensions: []*durable.Extension{new(durable.Extension{Name: "skills"}), skills}}
		got := resolveWith(&AgentState{}, snapshot, stale, nil).Extensions
		if len(got) != 1 || got[0] != skills {
			t.Fatalf("got %q", extensionNamesOf(got))
		}
	})

	t.Run("skips uninstalled names and resolves them again once they are installed", func(t *testing.T) {
		local := CreateRegistry()
		mustInstall(t, local, coding)
		state := &AgentState{Extensions: &ExtensionSelection{Exact: true, Names: []string{"coding", "skills"}}}
		expectStrings(t, extensionNamesOf(resolveWith(state, local.Snapshot(), nil, nil).Extensions), []string{"coding"})
		mustInstall(t, local, skills)
		expectStrings(t, extensionNamesOf(resolveWith(state, local.Snapshot(), nil, nil).Extensions), []string{"coding", "skills"})
	})

	t.Run("replaces same-name tools in place, wraps the winner, then applies the filter", func(t *testing.T) {
		local := CreateRegistry()
		venvBash := registryTool("bash", func(tool *durable.ToolRegistration) { tool.Description = "venv bash" })
		var calls []string
		venv := new(durable.Extension{Name: "venv", Tools: []*durable.ToolRegistration{venvBash}})
		timing := new(durable.Extension{
			Name: "timing",
			Wraps: []durable.Wrap{
				WrapTool(bash, func(inner *durable.ToolRegistration) *durable.ToolRegistration {
					wrapped := *inner
					wrapped.Description = inner.Description + " (timed)"
					return &wrapped
				}),
				WrapTool(bash, func(inner *durable.ToolRegistration) *durable.ToolRegistration {
					wrapped := *inner
					wrapped.Description = inner.Description + " [2]"
					return &wrapped
				}),
				// No grep is selected: the wrapper does nothing and reports nothing.
				WrapTool(registryTool("grep"), func(*durable.ToolRegistration) *durable.ToolRegistration {
					calls = append(calls, "grep")
					return registryTool("grep")
				}),
			},
		})
		mustInstall(t, local, coding, venv, timing)
		var reports []error
		agent := resolveWith(nil, local.Snapshot(), nil, &reports)
		var described [][2]string
		for _, tool := range agent.Tools {
			described = append(described, [2]string{tool.Name, tool.Description})
		}
		if want := [][2]string{{"read", "read tool"}, {"bash", "venv bash (timed) [2]"}, {"edit", "edit tool"}}; !reflect.DeepEqual(described, want) {
			t.Fatalf("got %q, want %q", described, want)
		}
		if len(reports) != 0 || len(calls) != 0 {
			t.Fatalf("reports %v calls %v", reports, calls)
		}

		// An array keeps exactly these names in its order, a repeated name at its first position.
		filtered := resolveWith(&AgentState{Tools: &ToolSelection{Exact: true, Names: []string{"edit", "missing", "read", "edit"}}}, local.Snapshot(), nil, nil)
		expectStrings(t, toolNamesOf(filtered.Tools), []string{"edit", "read"})
		removed := resolveWith(&AgentState{Tools: &ToolSelection{Remove: []string{"bash"}}}, local.Snapshot(), nil, nil)
		expectStrings(t, toolNamesOf(removed.Tools), []string{"read", "edit"})
	})

	t.Run("drops a tool or section whose wrapper throws or renames it and reports the failure", func(t *testing.T) {
		local := CreateRegistry()
		broken := new(durable.Extension{
			Name: "broken",
			Wraps: []durable.Wrap{
				WrapTool(read, func(*durable.ToolRegistration) *durable.ToolRegistration { panic(errors.New("wrapper failed")) }),
				WrapTool(edit, func(inner *durable.ToolRegistration) *durable.ToolRegistration {
					wrapped := *inner
					wrapped.Name = "renamed"
					return &wrapped
				}),
				WrapSection("cwd", func(*durable.PromptSection) *durable.PromptSection { panic(errors.New("section wrapper failed")) }),
			},
		})
		mustInstall(t, local, coding, broken)
		var reports []error
		agent := resolveWith(nil, local.Snapshot(), nil, &reports)
		expectStrings(t, toolNamesOf(agent.Tools), []string{"bash"})
		var keys []string
		for _, section := range agent.Sections {
			keys = append(keys, section.Key)
		}
		expectStrings(t, keys, []string{"preamble"})
		var messages []string
		for _, err := range reports {
			messages = append(messages, err.Error())
		}
		expectStrings(t, messages, []string{"wrapper failed", "Wrapper renamed edit to renamed", "section wrapper failed"})
	})

	t.Run("orders sections by extension, replaces same keys in place, and renders instructions last and unwrapped", func(t *testing.T) {
		local := CreateRegistry()
		override := new(durable.Extension{
			Name:     "override",
			Sections: []*durable.PromptSection{Section("preamble", text("You review."), SectionOptions{Tag: new(false)})},
			Wraps: []durable.Wrap{
				WrapSection("cwd", func(inner *durable.PromptSection) *durable.PromptSection {
					wrapped := *inner
					wrapped.Render = func(ctx context.Context, input durable.PromptInput) (*string, error) {
						rendered, err := inner.Render(ctx, input)
						if err != nil {
							return nil, err
						}
						return new(*rendered + "!"), nil
					}
					return &wrapped
				}),
				// Instructions are not wrapped.
				WrapSection("instructions", func(*durable.PromptSection) *durable.PromptSection { panic(errors.New("never")) }),
			},
		})
		mustInstall(t, local, coding, skills, override)
		var reports []error
		agent := resolveWith(&AgentState{Instructions: new("Be terse.")}, local.Snapshot(), nil, &reports)
		got := renderedOf(t, agent)
		want := []renderedSection{{"preamble", new("You review.")}, {"cwd", new("/repo!")}, {"skills", new("S")}, {"instructions", new("Be terse.")}}
		if len(got) != len(want) {
			t.Fatalf("rendered %d sections", len(got))
		}
		for i := range got {
			if got[i].key != want[i].key || *got[i].text != *want[i].text {
				t.Fatalf("section %d: got %s=%q, want %s=%q", i, got[i].key, *got[i].text, want[i].key, *want[i].text)
			}
		}
		if agent.Sections[len(agent.Sections)-1].Tag != nil {
			t.Fatal("instructions tag is set")
		}
		if len(reports) != 0 {
			t.Fatalf("reports %v", reports)
		}
	})

	t.Run("collects hooks of the selected extensions in extension order and applies field defaults", func(t *testing.T) {
		local := CreateRegistry()
		first := &ToolHooks{BeforeTool: func(context.Context, ai.ToolCall, HookApi) (*BeforeToolResult, error) { return nil, nil }}
		second := &ToolHooks{BeforeTool: func(context.Context, ai.ToolCall, HookApi) (*BeforeToolResult, error) { return nil, nil }}
		onYield := &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (*YieldContinue, error) { return nil, nil }}
		mustInstall(t, local,
			new(durable.Extension{Name: "a", Hooks: []durable.HookRegistration{Hook(ToolTask, first), Hook(GenerationTask, onYield)}}),
			new(durable.Extension{Name: "b", Hooks: []durable.HookRegistration{Hook(ToolTask, second)}}),
		)
		expectHooks := func(got []any, want ...any) {
			t.Helper()
			if len(got) != len(want) {
				t.Fatalf("hooks %d, want %d", len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("hook %d differs", i)
				}
			}
		}
		expectHooks(AgentHooks(resolveWith(nil, local.Snapshot(), nil, nil), "pi.tool"), first, second)
		ba := &AgentState{Extensions: &ExtensionSelection{Exact: true, Names: []string{"b", "a"}}}
		expectHooks(AgentHooks(resolveWith(ba, local.Snapshot(), nil, nil), "pi.tool"), second, first)
		onlyB := &AgentState{Extensions: &ExtensionSelection{Exact: true, Names: []string{"b"}}}
		expectHooks(AgentHooks(resolveWith(onlyB, local.Snapshot(), nil, nil), "pi.generation"))

		defaults := resolveWith(nil, local.Snapshot(), nil, nil)
		if defaults.Model != nil || defaults.ThinkingLevel != "off" || defaults.Cwd != nil {
			t.Fatalf("defaults %+v", defaults)
		}
		configured := resolveWith(&AgentState{Model: &durable.ModelRef{Provider: "p", ModelId: "m"}, ThinkingLevel: "high", Cwd: new("/w")}, local.Snapshot(), nil, nil)
		if *configured.Model != (durable.ModelRef{Provider: "p", ModelId: "m"}) || configured.ThinkingLevel != "high" || *configured.Cwd != "/w" {
			t.Fatalf("configured %+v", configured)
		}
	})
}

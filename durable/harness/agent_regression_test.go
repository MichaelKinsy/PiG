package harness

// pi: packages/durable/src/harness/agent.ts

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// A wrapper that returns no value makes upstream's nameOf(wrapped) throw inside applyWrap's try (agent.ts:240-246), so the target is dropped and the failure reported; resolution itself never fails.
func TestResolveAgentDropsTargetOfWrapperReturningNil(t *testing.T) {
	read := registryTool("read")
	coding := new(durable.Extension{
		Name:     "coding",
		Tools:    []*durable.ToolRegistration{read, registryTool("bash"), registryTool("edit")},
		Sections: []*durable.PromptSection{Section("preamble", text("You code.")), Section("cwd", text("/repo"))},
	})
	local := CreateRegistry()
	broken := new(durable.Extension{
		Name: "broken",
		Wraps: []durable.Wrap{
			WrapTool(read, func(*durable.ToolRegistration) *durable.ToolRegistration { return nil }),
			WrapSection("cwd", func(*durable.PromptSection) *durable.PromptSection { return nil }),
		},
	})
	mustInstall(t, local, coding, broken)
	var reports []error
	agent := resolveWith(nil, local.Snapshot(), nil, &reports)
	expectStrings(t, toolNamesOf(agent.Tools), []string{"bash", "edit"})
	var keys []string
	for _, section := range agent.Sections {
		keys = append(keys, section.Key)
	}
	expectStrings(t, keys, []string{"preamble"})
	if len(reports) != 2 {
		t.Fatalf("reports %v", reports)
	}
}

// addTools in a later commit than the configure: an array gains the names it lacks, a stored {remove} filter loses the added names, and unset tools stay unset (agent.ts:69-83; harness-tools.test.ts:559-563, 891-892).
func TestAddToolsEditsTheStoredToolFilter(t *testing.T) {
	extra, stop := supportTool("extra"), supportTool("stop")
	cases := []struct {
		name   string
		change *ToolChange
		want   any
	}{
		{"array", &ToolChange{Exact: true, List: []*durable.ToolRegistration{stop, extra}}, []any{"stop", "extra", "grow"}},
		{"remove filter", &ToolChange{Remove: []*durable.ToolRegistration{extra, supportTool("other")}}, map[string]any{"remove": []any{"other"}}},
		{"remove filter emptied", &ToolChange{Remove: []*durable.ToolRegistration{extra}}, map[string]any{"remove": []any{}}},
		{"unset", nil, nil},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
			if item.change != nil {
				if err := root.Configure(testContext, AgentChange{Tools: SetTo(*item.change)}); err != nil {
					t.Fatal(err)
				}
			}
			commitValue(t, root, func(tx durable.Tx) (any, error) {
				return nil, AddTools(tx, root.Id(), []string{"extra", "grow"})
			})
			state := must(durable.Snapshot[AgentState](testContext, harness, AgentDoc, root.Id()))
			var got any
			if state != nil && state.Tools != nil {
				got = jsonOf(t, state.Tools)
			}
			expectSameJSON(t, got, item.want)
			closeHarness(t, harness)
		})
	}
}

// src/harness/registry.ts:45 snapshot.sections(): every installed section with its extension, in install order, names repeating across
// extensions; a replaced extension keeps its place and an uninstalled one drops out.
// Pi source: packages/durable/src/harness/registry.ts
// mutation-checked: zeroing the results of RegistryReader.Snapshot fails it
// Pi: packages/durable/src/harness/agent.ts:170 (sections)
// packages/durable/src/harness/types.ts:283: RegistrySnapshot.sections() lists the sections of the installed extensions in install order.
func TestRegistrySnapshotSectionsInInstallOrder(t *testing.T) {
	local := CreateRegistry()
	first := &durable.Extension{Name: "first", Sections: []*durable.PromptSection{Section("preamble", text("a")), Section("cwd", text("/repo"))}}
	second := &durable.Extension{Name: "second", Sections: []*durable.PromptSection{Section("cwd", text("/other"))}}
	mustInstall(t, local, first, second)

	summary := func() []string {
		var got []string
		for _, installed := range local.Snapshot().Sections() {
			got = append(got, installed.Extension.Name+":"+installed.Section.Key)
		}
		return got
	}
	expectStrings(t, summary(), []string{"first:preamble", "first:cwd", "second:cwd"})

	replaced := &durable.Extension{Name: "first", Sections: []*durable.PromptSection{Section("mood", text("terse"))}}
	mustInstall(t, local, replaced)
	expectStrings(t, summary(), []string{"first:mood", "second:cwd"})

	local.Uninstall(second)
	expectStrings(t, summary(), []string{"first:mood"})
}

// agent.ts:19-30,53-66: the built-in policies are { enabled: true, maxRetries: 3, baseDelayMs: 2000, maxAgentDelayMs: 60000 } and
// { enabled: true, reserveTokens: 16384, keepRecentTokens: 20000, backgroundTokens: 32768 }; resolveSettings spreads them, so a partial
// override keeps the other fields and a resolved policy shares no storage with the default.
func TestDefaultPoliciesAndResolveSettingsMergeLikePi(t *testing.T) {
	if p := DefaultRetryPolicy; !p.Enabled || p.MaxRetries != 3 || p.BaseDelayMs != 2000 || p.MaxAgentDelayMs == nil || *p.MaxAgentDelayMs != 60000 {
		t.Fatalf("DefaultRetryPolicy = %+v", p)
	}
	if p := DefaultCompactionPolicy; !p.Enabled || p.ReserveTokens != 16384 || p.KeepRecentTokens != 20000 || p.BackgroundTokens != 32768 {
		t.Fatalf("DefaultCompactionPolicy = %+v", p)
	}
	resolved := ResolveSettings(nil)
	if resolved.Retry.MaxRetries != 3 || resolved.Compaction.ReserveTokens != 16384 {
		t.Fatalf("defaults not applied: %+v", resolved)
	}
	*resolved.Retry.MaxAgentDelayMs = 1
	if *DefaultRetryPolicy.MaxAgentDelayMs != 60000 || *ResolveSettings(nil).Retry.MaxAgentDelayMs != 60000 {
		t.Fatal("a resolved retry policy aliases the default's MaxAgentDelayMs")
	}
	maxRetries, reserve, disabled := 7, 100, false
	merged := ResolveSettings(&HarnessSettings{Retry: &RetryPolicyPatch{MaxRetries: &maxRetries}, Compaction: &CompactionPolicyPatch{ReserveTokens: &reserve, Enabled: &disabled}})
	if merged.Retry.MaxRetries != 7 || merged.Retry.BaseDelayMs != 2000 || !merged.Retry.Enabled || *merged.Retry.MaxAgentDelayMs != 60000 {
		t.Fatalf("retry merge = %+v", merged.Retry)
	}
	if merged.Compaction.ReserveTokens != 100 || merged.Compaction.Enabled || merged.Compaction.KeepRecentTokens != 20000 || merged.Compaction.BackgroundTokens != 32768 {
		t.Fatalf("compaction merge = %+v", merged.Compaction)
	}
}

func wireObject(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal(encoded, &keys); err != nil {
		t.Fatal(err)
	}
	return keys
}

// The harness task wire types are persisted checkpoints and inputs, so their JSON keys are the contract with Pi's store:
// compaction.ts:34-49 (SummaryRequest; CompactionCheckpoint is { phase: "select" } or the request flattened with phase, retry adding until),
// generation.ts GenerationResult { entryId }, tool.ts ToolTaskInput { assistant, callId }.
func TestHarnessTaskWireTypesUsePiKeys(t *testing.T) {
	request := &SummaryRequest{Attempt: 2, ThinkingLevel: "high", MaxTokens: 100, Tail: 7, FirstKept: 3}
	until := 1234.0
	for _, c := range []struct {
		name  string
		value any
		keys  []string
		not   []string
	}{
		{"select checkpoint has no request members", CompactionCheckpoint{Phase: "select"}, []string{"phase"}, []string{"attempt", "until", "model", "tail"}},
		{"summarize checkpoint flattens the request", CompactionCheckpoint{Phase: "summarize", SummaryRequest: request}, []string{"phase", "attempt", "model", "thinkingLevel", "streamOptions", "maxTokens", "tail", "firstKept"}, []string{"until", "SummaryRequest"}},
		{"retry checkpoint adds until", CompactionCheckpoint{Phase: "retry", SummaryRequest: request, Until: &until}, []string{"phase", "attempt", "model", "thinkingLevel", "streamOptions", "maxTokens", "tail", "firstKept", "until"}, []string{"SummaryRequest"}},
		{"summary request", *request, []string{"attempt", "model", "thinkingLevel", "streamOptions", "maxTokens", "tail", "firstKept"}, nil},
		{"generation result", GenerationResult{EntryId: 9}, []string{"entryId"}, nil},
		{"tool task input", ToolTaskInput{Assistant: 4, CallId: "c1"}, []string{"assistant", "callId"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := wireObject(t, c.value)
			for _, key := range c.keys {
				if _, ok := got[key]; !ok {
					t.Errorf("missing key %q in %v", key, got)
				}
			}
			for _, key := range c.not {
				if _, ok := got[key]; ok {
					t.Errorf("unexpected key %q in %v", key, got)
				}
			}
			if len(got) != len(c.keys) {
				t.Errorf("keys = %v, want exactly %v", got, c.keys)
			}
		})
	}
}

// define.ts:5-10 defineExtension is the identity function: the extension keeps every field. The Go registry compares extensions by
// pointer, so each call returns its own pointer and a later change to the argument does not reach the defined extension.
func TestDefineExtensionKeepsTheExtensionAndGivesEachCallItsOwnPointer(t *testing.T) {
	tool := registryTool("read")
	section := Section("preamble", text("hi"))
	extension := durable.Extension{Name: "coding", Tools: []*durable.ToolRegistration{tool}, Sections: []*durable.PromptSection{section}}
	define := DefineExtension // a function value: the test exercises DefineExtension itself, not its //go:fix inline expansion
	first, second := define(extension), define(extension)
	if first == second {
		t.Fatal("two DefineExtension calls share one pointer")
	}
	if first.Name != "coding" || len(first.Tools) != 1 || first.Tools[0] != tool || len(first.Sections) != 1 || first.Sections[0] != section {
		t.Fatalf("extension changed: %+v", first)
	}
	extension.Name = "changed"
	if first.Name != "coding" {
		t.Fatal("DefineExtension aliases its argument")
	}
}

// generation.ts:49-90 phase is "prepare" | "request" | "retry" | "poll" | "tools"; tool.ts:36-38 phase is "call" | "execute". The checkpoint
// fields are the named GenerationPhase and ToolTaskPhase, and every phase handler is registered under exactly one of those literals.
func TestCheckpointPhasesAreTheClosedPiUnions(t *testing.T) {
	for phase, literal := range map[GenerationPhase]string{generationPrepare: "prepare", generationRequest: "request", generationRetry: "retry", generationPoll: "poll", generationTools: "tools"} {
		if string(phase) != literal {
			t.Errorf("generation phase %q, want %q", phase, literal)
		}
	}
	for phase, literal := range map[ToolTaskPhase]string{toolPhaseCall: "call", toolPhaseExecute: "execute"} {
		if string(phase) != literal {
			t.Errorf("tool phase %q, want %q", phase, literal)
		}
	}
	if reflect.TypeOf(GenerationCheckpoint{}.Phase) != reflect.TypeFor[GenerationPhase]() || reflect.TypeOf(ToolTaskCheckpoint{}.Phase) != reflect.TypeFor[ToolTaskPhase]() {
		t.Fatal("checkpoint phase fields are not the named union types")
	}
	if got := wireObject(t, ToolTaskCheckpoint{Phase: toolPhaseCall})["phase"]; got != "call" {
		t.Fatalf("call checkpoint phase = %v", got)
	}
	if got := wireObject(t, GenerationCheckpoint{Phase: generationPoll})["phase"]; got != "poll" {
		t.Fatalf("poll checkpoint phase = %v", got)
	}
}

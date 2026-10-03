package harness

import (
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

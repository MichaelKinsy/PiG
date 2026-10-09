package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Pi harness/types.ts:263-265 Wrap = { tool; wrap(tool) } | { section; wrap(section) } and agent.ts:177-190 (`"tool" in wrap` wraps the named tool, otherwise the named
// section): the Go union is ToolWrap | SectionWrap, WrapTool and WrapSection build its members, and each member targets only its own kind; a wrapper without a function does nothing.
// mutation-checked: dispatching a ToolWrap onto sections (or the reverse) and applying a nil wrapper fail this test.
func TestWrapIsTheToolOrSectionUnion(t *testing.T) {
	tool := registryTool("bash")
	toolWrap := WrapTool(tool, func(inner *durable.ToolRegistration) *durable.ToolRegistration {
		wrapped := *inner
		wrapped.Description = "wrapped"
		return &wrapped
	})
	if member, ok := toolWrap.(durable.ToolWrap); !ok || member.Tool != "bash" || member.Wrap == nil {
		t.Fatalf("WrapTool built %#v, want a ToolWrap targeting bash", toolWrap)
	}
	sectionWrap := WrapSection("cwd", func(inner *durable.PromptSection) *durable.PromptSection { return inner })
	if member, ok := sectionWrap.(durable.SectionWrap); !ok || member.Section != "cwd" || member.Wrap == nil {
		t.Fatalf("WrapSection built %#v, want a SectionWrap targeting cwd", sectionWrap)
	}
	local := CreateRegistry()
	extension := new(durable.Extension{
		Name:  "wrapped",
		Tools: []*durable.ToolRegistration{tool},
		Wraps: []durable.Wrap{toolWrap, durable.ToolWrap{Tool: "bash"}, durable.SectionWrap{Section: "bash"}},
	})
	mustInstall(t, local, extension)
	var reports []error
	agent := resolveWith(nil, local.Snapshot(), nil, &reports)
	if len(agent.Tools) != 1 || agent.Tools[0].Description != "wrapped" || len(reports) != 0 {
		t.Fatalf("tools %+v reports %v, want the one tool wrapped and no report", agent.Tools, reports)
	}
}

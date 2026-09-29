package harness_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

func TestUpstreamHarnessResourceFormatting(t *testing.T) {
	// .upstream/v0.87.1/packages/agent/test/harness/resource-formatting.test.ts:6
	t.Run("formats skill invocations with additional instructions", func(t *testing.T) {
		skill := harness.Skill{Name: "inspect", Description: "Inspect things", Content: "Use inspection tools.", FilePath: "/project/.pi/skills/inspect/SKILL.md"}
		got := harness.FormatSkillInvocation(skill, "Check errors.")
		want := "<skill name=\"inspect\" location=\"/project/.pi/skills/inspect/SKILL.md\">\nReferences are relative to /project/.pi/skills/inspect.\n\nUse inspection tools.\n</skill>\n\nCheck errors."
		if got != want {
			t.Fatalf("invocation=%q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/resource-formatting.test.ts:19
	t.Run("formats prompt template invocations with positional arguments", func(t *testing.T) {
		got := harness.FormatPromptTemplateInvocation(harness.PromptTemplate{Name: "review", Content: "Review $1 with $ARGUMENTS"}, []string{"a.ts", "care"})
		if want := "Review a.ts with a.ts care"; got != want {
			t.Fatalf("invocation=%q; want %q", got, want)
		}
	})
}

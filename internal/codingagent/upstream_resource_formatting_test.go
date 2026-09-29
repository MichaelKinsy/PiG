package codingagent

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

func TestUpstreamResourceFormatting(t *testing.T) {
	// .upstream/v0.87.1/packages/agent/test/harness/resource-formatting.test.ts:6
	t.Run("formats skill invocations with additional instructions", func(t *testing.T) {
		path := filepath.FromSlash("/project/.pi/skills/inspect/SKILL.md")
		skill := harness.Skill{Name: "inspect", Description: "Inspect things", Content: "Use inspection tools.", FilePath: path}
		got := harness.FormatSkillInvocation(skill, "Check errors.")
		want := "<skill name=\"inspect\" location=\"" + path + "\">\nReferences are relative to " + filepath.Dir(path) + ".\n\nUse inspection tools.\n</skill>\n\nCheck errors."
		if got != want {
			t.Fatalf("invocation = %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/resource-formatting.test.ts:19
	t.Run("formats prompt template invocations with positional arguments", func(t *testing.T) {
		got := SubstitutePromptArgs("Review $1 with $ARGUMENTS", []string{"a.ts", "care"})
		if want := "Review a.ts with a.ts care"; got != want {
			t.Fatalf("invocation = %q; want %q", got, want)
		}
	})
}

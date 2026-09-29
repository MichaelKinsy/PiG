package harness_test

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/env"
)

func BenchmarkHarnessResourceLoading(b *testing.B) {
	e := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: b.TempDir()})
	const count = 64
	for i := range count {
		for path, body := range map[string]string{
			fmt.Sprintf("prompts/p%d.md", i):      "---\ndescription: Review code\n---\nReview $1 with $ARGUMENTS",
			fmt.Sprintf("skills/s%d/SKILL.md", i): "---\ndescription: Review the selected code\n---\nInspect source and tests.",
		} {
			if err := e.WriteFile(b.Context(), path, []byte(body)); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.Run("prompts", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			values, diagnostics := harness.LoadPromptTemplates(b.Context(), e, []string{"prompts"})
			if len(values) != count || len(diagnostics) != 0 {
				b.Fatal("incomplete prompt load")
			}
		}
	})
	b.Run("skills", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			values, diagnostics := harness.LoadSkills(b.Context(), e, []string{"skills"})
			if len(values) != count || len(diagnostics) != 0 {
				b.Fatal("incomplete skill load")
			}
		}
	})
}

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func BenchmarkSkillResourceMetadata(b *testing.B) {
	for _, count := range []int{1, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root := b.TempDir()
			cwd, agent := filepath.Join(root, "project"), filepath.Join(root, "agent")
			b.Setenv("HOME", root)
			b.Setenv("USERPROFILE", root)
			b.Setenv("PIG_HOME", filepath.Join(root, "home"))
			b.Setenv("PIG_CODING_AGENT_DIR", agent)
			b.Setenv("PI_CODING_AGENT_DIR", agent)
			b.Setenv("PI_OFFLINE", "1")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				b.Fatal(err)
			}
			for i := range count {
				dir := filepath.Join(agent, "skills", fmt.Sprintf("skill-%04d", i))
				if err := os.MkdirAll(dir, 0o755); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\ndescription: Skill\n---\nBody"), 0o644); err != nil {
					b.Fatal(err)
				}
			}
			provider := resourceSourceInfoProvider(cwd, agent, codingagent.NewSettingsManager(cwd, agent), Args{})
			b.ReportAllocs()
			for b.Loop() {
				if got := len(provider()); got != count {
					b.Fatalf("metadata entries=%d, want input skill count %d", got, count)
				}
			}
		})
	}
}

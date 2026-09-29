package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func BenchmarkPackageAutoloadResolution(b *testing.B) {
	for _, count := range []int{1, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root := b.TempDir()
			cwd, agent, pkg := filepath.Join(root, "project"), filepath.Join(root, "agent"), filepath.Join(root, "package")
			b.Setenv("HOME", root)
			b.Setenv("PIG_HOME", filepath.Join(root, "home"))
			b.Setenv("PIG_CODING_AGENT_DIR", agent)
			for _, dir := range []string{cwd, agent, filepath.Join(pkg, "prompts")} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					b.Fatal(err)
				}
			}
			for i := range count {
				if err := os.WriteFile(filepath.Join(pkg, "prompts", fmt.Sprintf("%04d.md", i)), []byte("Prompt"), 0o644); err != nil {
					b.Fatal(err)
				}
			}
			sm := codingagent.NewSettingsManager(cwd, agent)
			if err := sm.SetPackages([]codingagent.PackageSource{{Source: pkg}}); err != nil {
				b.Fatal(err)
			}
			if err := sm.SetProjectPackages([]codingagent.PackageSource{{Source: pkg, Autoload: new(false), Prompts: []string{"-prompts/0000.md"}}}); err != nil {
				b.Fatal(err)
			}
			provider := resourceSourceInfoProvider(cwd, agent, sm, CLIFlags{})
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				got := provider()
				if len(got) != count {
					b.Fatalf("resources=%d want input count%d", len(got), count)
				}
				first := got[filepath.Join(pkg, "prompts/0000.md")]
				if first.Enabled || first.Scope != "project" {
					b.Fatalf("delta=%+v", first)
				}
			}
		})
	}
}

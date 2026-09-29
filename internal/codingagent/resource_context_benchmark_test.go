package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func BenchmarkExtensionResourcePaths(b *testing.B) {
	for _, count := range []int{0, 32, 1024} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			cwd := b.TempDir()
			paths := &extension.ResourcesDiscoverAggregateResult{}
			for i := range count {
				paths.SkillPaths = append(paths.SkillPaths, extension.AttributedResourcePath{Path: fmt.Sprintf("extra skills/skill-%d", i), ExtensionPath: "extension.ts"})
			}
			b.ReportAllocs()
			for b.Loop() {
				result, err := NormalizeExtensionPaths(cwd, paths)
				if err != nil || len(result.SkillPaths) != count {
					b.Fatalf("normalize paths = %#v, %v", result, err)
				}
			}
		})
	}
}

func BenchmarkContextResourceReload(b *testing.B) {
	for _, depth := range []int{0, 8, 128} {
		b.Run(fmt.Sprintf("ancestors-%d", depth), func(b *testing.B) {
			root := b.TempDir()
			agentDir := filepath.Join(root, "agent")
			cwd := filepath.Join(root, "project")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				b.Fatal(err)
			}
			for range depth {
				cwd = filepath.Join(cwd, "d")
				if err := os.Mkdir(cwd, 0o755); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("Project instructions."), 0o600); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				if files := LoadProjectContextFiles(cwd, agentDir); len(files) != depth {
					b.Fatalf("loaded %d contexts, want one per ancestor (%d)", len(files), depth)
				}
			}
		})
	}
}

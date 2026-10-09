package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Pi core/skills.ts:434-508 retains discovery insertion order. A frontmatter
// name may differ from its directory and must not reorder the resulting list.
func TestSkillDiscoveryOrderSurvivesFrontmatterNamesAndReload(t *testing.T) {
	root := t.TempDir()
	for _, skill := range []struct{ dir, name string }{{"01-first", "zulu"}, {"02-second", "alpha"}} {
		dir := filepath.Join(root, skill.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+skill.name+"\ndescription: valid\n---\nbody"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"zulu", "alpha"}
	names := func(skills []*SkillDef) []string {
		out := make([]string, len(skills))
		for i, skill := range skills {
			out[i] = skill.Name
		}
		return out
	}
	t.Run("directory loader", func(t *testing.T) {
		loaded, err := LoadSkillsFromPath(root)
		if err != nil {
			t.Fatal(err)
		}
		if got := names(loaded); !reflect.DeepEqual(got, want) {
			t.Fatalf("discovery order = %v, want %v", got, want)
		}
	})
	t.Run("interactive reload", func(t *testing.T) {
		m := &InteractiveMode{opts: InteractiveModeOptions{SkillPaths: []string{root}}}
		m.reloadSkillsFromPaths()
		if got := names(m.opts.Skills); !reflect.DeepEqual(got, want) {
			t.Fatalf("reload order = %v, want %v", got, want)
		}
		if got := names(m.slashCatalog.Load().Skills); !reflect.DeepEqual(got, want) {
			t.Fatalf("published skill order = %v, want %v", got, want)
		}
	})
}

func BenchmarkSkillPathDiscovery(b *testing.B) {
	const count = 64
	root := b.TempDir()
	for i := range count {
		dir := filepath.Join(root, fmt.Sprintf("%03d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		body := fmt.Sprintf("---\nname: skill-%03d\ndescription: valid\n---\nbody", count-i)
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		skills, err := LoadSkillsFromPath(root)
		if err != nil || len(skills) != count {
			b.Fatalf("loaded=%d error=%v", len(skills), err)
		}
	}
}

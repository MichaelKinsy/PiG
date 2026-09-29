package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// upstream: packages/coding-agent/src/core/agent-session.ts:1797-1821 — each invocation reads the current file and trims only JavaScript whitespace.
func TestSkillExpansionReadsCurrentFileAndPreservesCommandParsing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-skill.md")
	skill := &SkillDef{Name: "test", Path: path, Dir: dir, Body: "discovery-time body"}
	for _, body := range []string{"", "first body", "second body", "\u0085body\u0085", "\ufeffbody\ufeff", strings.Repeat("large body\n", 65536)} {
		if err := os.WriteFile(path, []byte("---\nname: test\n---\n\n"+body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, args := range []string{"", " explain this ", "\uFEFFextra\uFEFF", "\u0085kept\u0085"} {
			command := "/skill:test"
			if args != "" {
				command += " " + args
			}
			got, ok, failure := ExpandSkillCommand(command, []*SkillDef{skill})
			expected := fmt.Sprintf("<skill name=\"test\" location=\"%s\">\nReferences are relative to %s.\n\n%s\n</skill>", path, dir, strings.Trim(body, "\n\ufeff"))
			if args != "" {
				expected += "\n\n" + strings.Trim(args, " \uFEFF")
			}
			if !ok || failure != nil || got != expected {
				t.Fatalf("body bytes=%d args=%q: ok=%v error=%v output matches=%v", len(body), args, ok, failure, got == expected)
			}
		}
	}
	for _, command := range []string{"", "ordinary", "/skill:missing", "/skill:test\targs", "/skill:test\uFEFF"} {
		got, ok, failure := ExpandSkillCommand(command, []*SkillDef{skill})
		if ok || failure != nil || got != "" {
			t.Fatalf("unknown %q expanded: %q, %v, %v", command, got, ok, failure)
		}
	}
	if skill.Body != "discovery-time body" {
		t.Fatal("invocation changed discovery metadata")
	}
}

func TestSkillExpansionReportsFileFailureWithoutCachedBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.md")
	got, ok, failure := ExpandSkillCommand("/skill:test explain", []*SkillDef{{Name: "test", Path: path, Body: "cached body"}})
	if ok || got != "" || failure == nil || failure.ExtensionPath != path || failure.Event != "skill_expansion" || failure.Error != "ENOENT: no such file or directory, open '"+path+"'" {
		t.Fatalf("expansion=%q matched=%v failure=%+v", got, ok, failure)
	}
}

func BenchmarkSkillInvocation(b *testing.B) {
	for _, size := range []int{0, 64, 65536, 1048576} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "SKILL.md")
			if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o600); err != nil {
				b.Fatal(err)
			}
			skills := []*SkillDef{{Name: "test", Path: path, Dir: filepath.Dir(path)}}
			b.ReportAllocs()
			for b.Loop() {
				if _, ok, failure := ExpandSkillCommand("/skill:test args", skills); !ok || failure != nil {
					b.Fatal(failure)
				}
			}
		})
	}
}

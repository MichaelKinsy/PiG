package codemode_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/codemode"
)

func catalogOf(n int) []codemode.Tool {
	tools := make([]codemode.Tool, n)
	for i := range tools {
		tools[i].Name = fmt.Sprintf("tool_%06d", i)
	}
	return tools
}

// Pi: packages/codemode/src/runtime/host.ts registerTool keeps tools in a Map by name: a duplicate throws, the first
// registration keeps its place, and an unregistered name can be registered again at the end.
func TestSandboxConstructionKeepsOrderAndRejectsDuplicates(t *testing.T) {
	tools := catalogOf(5)
	s, err := codemode.NewSandbox(codemode.SandboxOptions{Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.RegisterTool(codemode.Tool{Name: "tool_000002"}); err == nil || !strings.Contains(err.Error(), `Tool "tool_000002" is already registered`) {
		t.Fatalf("duplicate RegisterTool error = %v", err)
	}
	if !s.UnregisterTool("tool_000001") || s.UnregisterTool("tool_000001") {
		t.Fatal("UnregisterTool must report true once, then false")
	}
	if err := s.RegisterTool(codemode.Tool{Name: "tool_000001"}); err != nil {
		t.Fatalf("re-registering an unregistered name: %v", err)
	}
	var got []string
	for _, tool := range s.Tools() {
		got = append(got, tool.Name)
	}
	want := "tool_000000 tool_000002 tool_000003 tool_000004 tool_000001"
	if strings.Join(got, " ") != want {
		t.Fatalf("Tools() = %v, want %s", got, want)
	}
	dup := append(catalogOf(3), codemode.Tool{Name: "tool_000001"})
	if _, err := codemode.NewSandbox(codemode.SandboxOptions{Tools: dup}); err == nil || !strings.Contains(err.Error(), `Tool "tool_000001" is already registered`) {
		t.Fatalf("NewSandbox with a duplicate = %v", err)
	}
}

// Invariant: sandbox construction is linear in the catalog. A per-insert scan is quadratic: 32 times more tools cost
// about 1000 times more; linear growth costs about 32 times more. The bound sits between, on the best of three runs.
func TestSandboxConstructionIsLinearInTheCatalogSize(t *testing.T) {
	build := func(n int) time.Duration {
		tools := catalogOf(n)
		best := time.Duration(1<<63 - 1)
		for range 3 {
			start := time.Now()
			s, err := codemode.NewSandbox(codemode.SandboxOptions{Tools: tools})
			elapsed := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			best = min(best, elapsed)
		}
		return best
	}
	small, large := max(build(2_000), 100*time.Microsecond), build(64_000)
	if ratio := float64(large) / float64(small); ratio > 300 {
		t.Fatalf("64000 tools took %s and 2000 took %s: %.0fx for 32x the tools, want linear growth", large, small, ratio)
	}
}

func BenchmarkSandboxConstruction(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		tools := catalogOf(n)
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				s, err := codemode.NewSandbox(codemode.SandboxOptions{Tools: tools})
				if err != nil {
					b.Fatal(err)
				}
				s.Close()
			}
		})
	}
}

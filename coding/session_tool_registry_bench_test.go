package coding

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

func BenchmarkSessionToolRegistry(b *testing.B) {
	for _, count := range []int{0, 64, 1024} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			services, err := NewServices(ServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
			if err != nil {
				b.Fatal(err)
			}
			definitions := make([]extension.ToolDefinition, count)
			for i := range definitions {
				definitions[i] = registryTool(fmt.Sprintf("custom_%d", i), "Custom", "Custom tool", fmt.Sprintf("Run custom operation %d", i))
			}
			opts := SessionOptions{Model: fakeModel(), NoSession: true, CustomTools: definitions}
			b.ReportAllocs()
			for b.Loop() {
				session, err := NewSession(services, opts)
				if err != nil {
					b.Fatal(err)
				}
				if len(session.GetAllTools()) != count+len(tools.BuiltinToolNames()) {
					b.Fatal("registry cardinality differs from builtin plus custom inputs")
				}
				if err := session.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

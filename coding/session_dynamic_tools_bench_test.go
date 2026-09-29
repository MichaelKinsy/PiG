package coding

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func BenchmarkSessionDynamicTools(b *testing.B) {
	for _, count := range []int{8, 128, 1024} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			services, err := NewServices(ServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
			if err != nil {
				b.Fatal(err)
			}
			ext := extension.Extension{Tools: map[string]extension.RegisteredTool{}}
			for i := range count {
				name := fmt.Sprintf("dynamic_%d", i)
				ext.Tools[name] = extension.RegisteredTool{Definition: registryTool(name, name, "dynamic tool", "Run "+name)}
				ext.ToolOrder = append(ext.ToolOrder, name)
			}
			ext.InitializeToolRegistry()
			runner := inproc.NewRunner([]extension.Extension{ext}, services.CWD())
			session, err := NewSession(services, SessionOptions{Model: fakeModel(), NoSession: true, Runner: runner, SkipBuiltinTools: true})
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			replacement := ext.RegisteredTools()[0]
			b.ReportAllocs()
			for b.Loop() {
				ext.SetRegisteredTool(replacement)
				if err := session.RefreshTools(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

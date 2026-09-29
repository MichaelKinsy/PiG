package tools

import (
	"context"
	"strings"
	"testing"

	envpkg "github.com/MichaelKinsy/PiG/agent/harness/env"
)

func BenchmarkHarnessReadBoundedOutput(b *testing.B) {
	env := envpkg.NewNodeExecutionEnv(envpkg.NodeExecutionEnvOptions{Cwd: b.TempDir()})
	b.Cleanup(func() { env.Cleanup(context.Background()) })
	content := strings.Repeat("line content\n", 4000)
	if err := env.WriteFile(b.Context(), "large.txt", []byte(content)); err != nil {
		b.Fatal(err)
	}
	tool := CreateReadTool(nil)
	turn := ExecutionToolContext{Env: env}
	params := map[string]any{"path": "large.txt"}
	b.SetBytes(int64(len(content)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := tool.Execute(b.Context(), "read", params, noUpdate, turn, testInvocation{}); err != nil {
			b.Fatal(err)
		}
	}
}

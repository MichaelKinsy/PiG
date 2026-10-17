//go:build !windows

package tools

import (
	"context"
	"encoding/json"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// tenMegabyteCommand writes 10 MiB of 41-byte lines as fast as the pipe takes them.
const tenMegabyteCommand = "yes 0123456789012345678901234567890123456789 | head -c 10485760"

func selfCPU(b *testing.B) time.Duration {
	b.Helper()
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		b.Fatal(err)
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

// BenchmarkBashOutput10MB is the CPU pig spends on 10 MiB of command output (the command's own CPU is not counted): through the bash tool with a
// streaming-update observer, and through user bash (`!cmd`) with a chunk observer.
func BenchmarkBashOutput10MB(b *testing.B) {
	b.Setenv("TMPDIR", b.TempDir())
	b.Run("tool", func(b *testing.B) {
		tool := &BashTool{CWD: b.TempDir()}
		args, _ := json.Marshal(map[string]any{"command": tenMegabyteCommand})
		onUpdate := func(agent.AgentToolResult) {}
		var cpu time.Duration
		for b.Loop() {
			start := selfCPU(b)
			if _, err := tool.Execute(context.Background(), "", args, onUpdate); err != nil {
				b.Fatal(err)
			}
			cpu += selfCPU(b) - start
		}
		b.ReportMetric(float64(cpu.Milliseconds())/float64(b.N), "cpu-ms/op")
	})
	b.Run("user", func(b *testing.B) {
		ops := CreateLocalBashOperations(nil)
		dir := b.TempDir()
		var cpu time.Duration
		for b.Loop() {
			start := selfCPU(b)
			if _, err := ExecuteBashWithOperations(context.Background(), tenMegabyteCommand, dir, ops, BashExecOptions{OnChunk: func(string) {}}); err != nil {
				b.Fatal(err)
			}
			cpu += selfCPU(b) - start
		}
		b.ReportMetric(float64(cpu.Milliseconds())/float64(b.N), "cpu-ms/op")
	})
}

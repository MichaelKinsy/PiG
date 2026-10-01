package codemode

import "testing"

// BenchmarkMemoryLimitTrip times a runaway allocation script from start to the out-of-memory error inside the
// script (the script of agent-session-codemode.test.ts "limits script memory so runaway allocations fail inside the
// script").
func BenchmarkMemoryLimitTrip(b *testing.B) {
	const script = "let a = [];\ntry { while (true) a.push(\"x\".repeat(1 << 20) + a.length); } catch (error) { const n = a.length; a = null; return { n, error: String(error) }; }"
	sandbox, err := NewSandbox(SandboxOptions{TimeoutMs: 30_000, MemoryLimitBytes: 256 << 20})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = sandbox.Close() })
	for b.Loop() {
		r, err := sandbox.Execute(b.Context(), script, ExecuteOptions{})
		if err != nil || !r.OK {
			b.Fatalf("%v %+v", err, r)
		}
	}
}

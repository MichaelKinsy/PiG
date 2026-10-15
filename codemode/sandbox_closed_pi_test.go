package codemode

import (
	"context"
	"testing"
)

// host.ts CodemodeSandbox.execute: `if (this.closed) return Promise.reject(new Error("Sandbox is closed"))`, and close() is idempotent in effect: every
// execute after it rejects with that message, whatever its options, and without starting a VM. Execute reports it as the error, never as a script Result.
func TestExecuteAfterCloseFailsWithPisMessageAndRunsNothing(t *testing.T) {
	sandbox, err := NewSandbox(SandboxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sandbox.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.Close(); err != nil {
		t.Fatalf("a second Close = %v, want nil", err)
	}
	for _, options := range []ExecuteOptions{{}, {TimeoutMs: 5}} {
		result, err := sandbox.Execute(context.Background(), "return 1", options)
		if err == nil || err.Error() != "Sandbox is closed" {
			t.Fatalf("Execute after Close = %v, want the error %q", err, "Sandbox is closed")
		}
		if result.OK || result.Value != nil || len(result.Output) != 0 {
			t.Fatalf("Execute after Close returned a result %+v, want none", result)
		}
	}
}

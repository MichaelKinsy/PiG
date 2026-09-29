//go:build parity

package runner

import (
	"context"
	"testing"
)

func TestOutputEqualRejectsUnjustifiedReplacement(t *testing.T) {
	o := &ScenarioOutcome{Scenario: &Scenario{Assert: AssertSpec{OutputEqual: true, NormalizeReplace: []NormalizeReplaceRule{{Pattern: `error:.*`, With: "error"}}}}, Pig: SystemResults{Runs: []Result{{Output: "error: lost history"}}}, Pi: SystemResults{Runs: []Result{{Output: "error: unknown command"}}}}
	EvaluateOutcome(o)
	if o.Passed() {
		t.Fatal("unjustified replacement hid a different error")
	}
}

func TestRPCDriverPreservesFraming(t *testing.T) {
	sc := &Scenario{Name: "framing", RPC: RPCDriverConfig{InputLines: []string{"request"}, TimeoutSeconds: 5}}
	bin := BinaryRef{Label: "pig", Path: "sh", Args: []string{"-c", `read -r line; printf '{"ok":true}\n\r '; printf 'diagnostic\n' >&2`}}
	result := (rpcModeDriver{}).Run(t.Context(), t, bin, sc)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Output != "{\"ok\":true}\n\r " {
		t.Fatalf("framing trimmed: %q", result.Output)
	}
}

func TestRPCWaitDoesNotAcceptOldOutput(t *testing.T) {
	var output synchronizedBuffer
	_, _ = output.Write([]byte("old ready\n"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := waitForRPCOutput(ctx, &output, len(output.String()), []string{"ready"}, "", 1); err == nil {
		t.Fatal("old output satisfied a new barrier")
	}
}

func TestPaneWaitRequiresEveryPatternToBeNew(t *testing.T) {
	if paneSatisfiesWait("old ready\nnew response", []string{"ready", "response"}, "old ready", true) {
		t.Fatal("stale ready plus new response satisfied barrier")
	}
}

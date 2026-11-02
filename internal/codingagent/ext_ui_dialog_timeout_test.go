package codingagent

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// extension-selector.ts:56 and extension-input.ts:60 hand the dialog's timeout in
// milliseconds, a JS number that may carry a fraction, to CountdownTimer, which
// shows Math.ceil(timeoutMs / 1000) seconds. A timeout a fraction of a nanosecond
// past a whole second must not truncate back to it.
func TestDialogTimeoutKeepsAFractionOfAMillisecondPastAWholeSecond(t *testing.T) {
	t.Parallel()
	timeout := 1000.0000004
	got := dialogTimeout(extension.ExtensionUIDialogOptions{Timeout: &timeout})
	if got <= time.Second {
		t.Fatalf("dialogTimeout(%vms) = %v, want more than 1s so the countdown starts at 2 (Math.ceil(%v/1000))", timeout, got, timeout)
	}
	whole := 1500.0
	if got := dialogTimeout(extension.ExtensionUIDialogOptions{Timeout: &whole}); got != 1500*time.Millisecond {
		t.Fatalf("dialogTimeout(1500ms) = %v, want 1.5s", got)
	}
}

// A wire integer outside int64's range is rejected rather than converted: Go leaves
// float-to-int conversion of such a value implementation-defined (arm64 saturates).
func TestSubprocessOptionalInt64RejectsAValueBeyondInt64(t *testing.T) {
	t.Parallel()
	for _, value := range []float64{9223372036854775808, 1e19, -9223372036854777856} {
		if got, err := subprocessOptionalInt64(map[string]any{"n": value}, "n", "request"); err == nil {
			t.Fatalf("subprocessOptionalInt64(%v) = %d, nil; want an integer-range error", value, got)
		}
	}
	if got, err := subprocessOptionalInt64(map[string]any{"n": float64(-9223372036854775808)}, "n", "request"); err != nil || got != -9223372036854775808 {
		t.Fatalf("subprocessOptionalInt64(-2^63) = %d, %v; want -2^63, nil", got, err)
	}
}

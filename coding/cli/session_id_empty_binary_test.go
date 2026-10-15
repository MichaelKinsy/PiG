package cli

import "testing"

// main.ts validateSessionIdFlags returns early only when --session-id is absent; an explicitly empty id fails assertValidSessionId, so Pi 1.1.0 'pi --session-id "" -p hi' exits 1 with this error before any model or session work.
func TestEmptySessionIDExitsWithUpstreamError(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	stdout, stderr, code := runPigForModeTest(t, bin, nil, nil, "--session-id", "", "-p", "hi")
	want := "Error: Session id must be non-empty, contain only alphanumeric characters, '-', '_', and '.', and start and end with an alphanumeric character\n"
	if code != 1 || stderr != want || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

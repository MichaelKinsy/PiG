package plugins

import "testing"

func TestDeprecatedRolloutHostAliases(t *testing.T) {
	if RolloutReported != RolloutReloaded {
		t.Fatalf("RolloutReported = %q, want %q", RolloutReported, RolloutReloaded)
	}
	if RolloutErrored != RolloutFailed {
		t.Fatalf("RolloutErrored = %q, want %q", RolloutErrored, RolloutFailed)
	}
	if !(RolloutHost{Kind: RolloutStopped}).Stopped() {
		t.Fatal("Stopped() = false for RolloutStopped, want true")
	}
	if (RolloutHost{Kind: RolloutReloaded}).Stopped() || (RolloutHost{Kind: RolloutFailed}).Stopped() {
		t.Fatal("Stopped() = true for non-stopped RolloutHost")
	}
}

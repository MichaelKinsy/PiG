package closure

import (
	"strings"
	"testing"
)

func TestVerifyWitnessArtifactClaimsRequiresTypedTraceArtifact(t *testing.T) {
	witness := &ExecutionWitness{WitnessType: "terminal-trace"}
	err := verifyWitnessArtifactClaims(t.TempDir(), t.TempDir(), witness)
	if err == nil || !strings.Contains(err.Error(), "typed trace witness requires one artifact") {
		t.Fatalf("verifyWitnessArtifactClaims() error = %v, want typed trace artifact requirement", err)
	}
}

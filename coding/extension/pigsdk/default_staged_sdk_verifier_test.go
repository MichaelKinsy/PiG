package pigsdk

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Linking the SDKs registers their staged-SDK check for library entry points: a program that can build Go extensions at all
// (it links this package) verifies the staged copy, whether or not it is the pig command.
func TestLinkingTheSDKsRegistersTheirStagedSDKCheck(t *testing.T) {
	verify := subprocess.DefaultStagedSDKVerifier(t.TempDir())
	if verify == nil {
		t.Fatal("linking pigsdk registered no staged-SDK check")
	}
	if _, ok := bundleFor("go"); !ok {
		t.Skip("this build strips the Go SDK")
	}
	if err := verify(t.TempDir(), "go"); err == nil || !strings.Contains(err.Error(), "no version marker") {
		t.Fatalf("verify(unmarked dir) = %v, want VerifyStaged's missing-marker error", err)
	}
}

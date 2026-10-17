package cli_test

import (
	"testing"

	_ "github.com/MichaelKinsy/PiG/coding/cli"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The pig command links the SDKs, which registers their staged-SDK check, so hosts that coding.DiscoverAndLoadExtensions starts
// inside it verify the staged SDK as the command's own extension hosts do.
func TestThePigCommandRegistersTheStagedSDKVerifier(t *testing.T) {
	if subprocess.DefaultStagedSDKVerifier(t.TempDir()) == nil {
		t.Fatal("importing the pig command registered no staged-SDK verifier")
	}
}

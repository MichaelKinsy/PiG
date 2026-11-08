package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The SentFiles contract's fingerprint equals Pi's (see sessions/contract).
func TestSentFilesContractFingerprintsAsPi(t *testing.T) {
	const module = "plugins/send-file/contract.ts"
	pi := oracle.Fingerprints(t, module)
	want, ok := pi[module+"#SentFiles"]
	if !ok {
		t.Fatal("Pi exports no SentFiles contract")
	}
	if SentFiles.Name != want.Name || SentFiles.Version != want.Version || SentFiles.Fingerprint != want.Fingerprint {
		t.Errorf("SentFiles = {%s v%d %s}, Pi has {%s v%d %s}", SentFiles.Name, SentFiles.Version, SentFiles.Fingerprint, want.Name, want.Version, want.Fingerprint)
	}
	if got := OfferedBy(3); got != "conversation:3" {
		t.Errorf("OfferedBy(3) = %q", got)
	}
}

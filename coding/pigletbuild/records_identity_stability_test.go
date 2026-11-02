package pigletbuild

import (
	"testing"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// A Piglet build record stores a local source's identity. Pi 1be312b4 (#10488) folds the case of Windows path keys where package identities are compared, not where they are
// stored, so the bytes of a record's identity are the same whether or not the platform folds case.
func TestRecordSourceIdentityBytesDoNotChangeWhenThePlatformFoldsCase(t *testing.T) {
	before, _, err := recordSource("local:./Pkg/Ext", sourceref.BareLocal, "/Work/Proj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	after, _, err := recordSource("local:./Pkg/Ext", sourceref.BareLocal, "/Work/Proj")
	if err != nil || after != before {
		t.Fatalf("identity = %q, %v on Windows; want the unchanged %q", after, err, before)
	}
}

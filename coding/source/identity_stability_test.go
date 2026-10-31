package source

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// Pi 1be312b4 (#10488) keys a local package identity by getPathComparisonKey. Ref.Identity feeds Piglet build records, so it keeps the resolved spelling on every platform;
// the comparison key is applied where identities are compared (packagemanager.PackageSourceIdentityChecked).
func TestLocalIdentityKeepsItsSpellingWhateverThePlatformFoldsCase(t *testing.T) {
	ref, err := Parse("local:./Pkg/Ext", Options{BaseDir: "/Work/Proj", Bare: BareLocal})
	if err != nil {
		t.Fatal(err)
	}
	before, err := ref.Identity("/Work/Proj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	after, err := ref.Identity("/Work/Proj")
	if err != nil || after != before {
		t.Fatalf("identity = %q, %v on Windows; want the unchanged %q", after, err, before)
	}
}

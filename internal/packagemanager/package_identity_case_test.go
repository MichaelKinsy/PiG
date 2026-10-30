package packagemanager

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// Pi 1be312b4 (#10488): local package matching (`pi remove c:\pkg` against a configured `C:\pkg`) and package identity across global and project settings ignore case on Windows.
func TestPackageSourcesMatchIgnoresLocalPathCasingOnWindows(t *testing.T) {
	if match, err := PackageSourcesMatch(`C:\work`, `C:\work`, `C:\pkg`, `c:\PKG`); err != nil || match {
		t.Fatalf("match = %v, %v; want no match on a case-sensitive platform", match, err)
	}
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	if match, err := PackageSourcesMatch(`C:\work`, `C:\work`, `C:\pkg`, `c:\PKG`); err != nil || !match {
		t.Fatalf("match = %v, %v; want the two spellings to name one package on Windows", match, err)
	}
	if PackageSourceIdentity(`C:\work`, `C:\pkg`) != PackageSourceIdentity(`C:\work`, `c:\PKG`) {
		t.Fatal("identities of one local package in two casings differ on Windows")
	}
}

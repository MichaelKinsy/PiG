package source

import "testing"

func TestValidNPMVersionAcceptsOnlyWhatNPMPublishes(t *testing.T) {
	for _, version := range []string{"0.0.0", "1.2.3", "10.20.30", "2.0.0-rc.1", "1.0.0-alpha.beta.1", "1.2.3+build.5", "1.2.3-rc.1+sha.abc"} {
		if !ValidNPMVersion(version) {
			t.Errorf("ValidNPMVersion(%q) = false, want true", version)
		}
	}
	// npm reports `Invalid version` for each of these; Go's semver accepts the first three with a leading v.
	for _, version := range []string{"1", "1.2", "1.2-rc.1", "v1.2.3", "", "1.2.3.4", "01.2.3", "1.2.3-01", "latest", " 1.2.3"} {
		if ValidNPMVersion(version) {
			t.Errorf("ValidNPMVersion(%q) = true, want false", version)
		}
	}
}

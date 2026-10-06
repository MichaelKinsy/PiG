package source

import (
	"strings"

	"golang.org/x/mod/semver"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// npmPackageName is npm's name grammar for a new package: an optional lowercase scope, then lowercase letters, digits, and `._~-` that do not start with `.` or `_`.
var npmPackageName = lazyregexp.New(`^(?:@[a-z0-9~][a-z0-9._~-]*/)?[a-z0-9~][a-z0-9._~-]*$`)

// ValidNPMPackageName reports whether name is a name npm accepts for a package that is about to be published. It does not check npm's length limit; npm reports that when it publishes.
//
// pig additive (D18): npm publication of Packages and Piglet source.
func ValidNPMPackageName(name string) bool {
	return npmPackageName.MatchString(name)
}

// ValidNPMVersion reports whether version is a version npm publishes: a full MAJOR.MINOR.PATCH semantic version with an optional prerelease and build, and no leading `v`. Go's semver also accepts the shorthand `1` and `1.2`, which npm rejects as an invalid version.
//
// pig additive (D18): npm publication of Packages and Piglet source.
func ValidNPMVersion(version string) bool {
	if strings.HasPrefix(version, "v") || !semver.IsValid("v"+version) {
		return false
	}
	release, _, _ := strings.Cut(version, "+")
	return semver.Canonical("v"+version) == "v"+release
}

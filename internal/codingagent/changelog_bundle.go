//go:build !pig_strip_changelog

package codingagent

// changelog_bundle.go and its import of the root package (the embedded
// CHANGELOG.md) are the boundary a Piglet Binary compiles out with the
// pig_strip_changelog tag; changelog_bundle_stripped.go replaces them.

import (
	pig "github.com/MichaelKinsy/PiG"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// bundledChangelog is the CHANGELOG.md embedded at build time, behind
// /changelog and the startup What's New. A Piglet that strips the changelog
// gets "", as its Binary does.
func bundledChangelog() string {
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.Changelog) {
		return ""
	}
	return pig.Changelog
}

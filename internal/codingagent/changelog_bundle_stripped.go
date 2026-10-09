//go:build pig_strip_changelog

package codingagent

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// pig additive (D92): this Piglet Binary compiled out the bundled changelog (the root package's CHANGELOG.md embed).
func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.Changelog) }

// bundledChangelog is empty: this build has no changelog, so the startup What's New never shows.
func bundledChangelog() string { return "" }

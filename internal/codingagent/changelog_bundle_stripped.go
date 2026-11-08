//go:build pig_strip_changelog

package codingagent

import (
	"io"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// pig additive (D92): this Piglet Binary compiled out the bundled changelog (the root package's CHANGELOG.md embed).
func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.Changelog) }

// bundledChangelog is empty: this build has no changelog, so the startup What's New never shows.
func bundledChangelog() string { return "" }

// bundledChangelogReader is empty for the same reason.
func bundledChangelogReader() io.Reader { return strings.NewReader("") }

//go:build pig_strip_docs

package cli

import (
	"io"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// pig additive (D92): this Piglet Binary compiled out the PiG documentation bundle.
func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.Docs) }

// runDocsCommand reports that `pig docs` is unavailable in this build.
func runDocsCommand(args []string, _, _ io.Writer) int {
	if len(args) == 0 || args[0] != "docs" {
		return -1
	}
	return reportStrippedFeature("pig docs", pigstrip.Docs, "")
}

// syncDocsBundle does nothing: this build has no documentation bundle.
func syncDocsBundle() {}

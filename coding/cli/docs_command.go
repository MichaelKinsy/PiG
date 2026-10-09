//go:build !pig_strip_docs

package cli

import (
	"io"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pigdocs"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// This file and its internal/pigdocs import (the embedded documentation
// bundle) are the boundary a Piglet Binary compiles out with the
// pig_strip_docs tag.

// runDocsCommand runs `pig docs` and returns its exit code, or -1 when args
// target another command.
func runDocsCommand(args []string, stdout, stderr io.Writer) int {
	// pig additive (D92): a Piglet that strips docs reports them absent, as its Binary does.
	if len(args) > 0 && args[0] == "docs" && pigstrip.Has(pigstrip.ListFeatures, pigstrip.Docs) {
		return reportStrippedFeature("pig docs", pigstrip.Docs, "")
	}
	return pigdocs.RunCommand(args, stdout, stderr)
}

// syncDocsBundle materializes the documentation bundle the system prompt's
// docs section names. A Piglet that strips docs writes none.
func syncDocsBundle() {
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.Docs) {
		return
	}
	_ = pigdocs.EnsureSynced(codingagent.ConfigRoot())
}

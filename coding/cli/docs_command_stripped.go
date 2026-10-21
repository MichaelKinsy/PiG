//go:build pig_strip_docs

package cli

import (
	"context"
	"encoding/json"
	"io"

	"github.com/MichaelKinsy/PiG/agent"
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

// prepareDocsBundle does nothing: this build has no documentation bundle.
func prepareDocsBundle() {}

// writeDocsBundle does nothing: this build has no documentation bundle.
func writeDocsBundle() {}

// writeDocsBundleBeforeToolCall lets every tool call proceed: this build has no documentation bundle.
func writeDocsBundleBeforeToolCall(context.Context, string, string, json.RawMessage) agent.ToolCallHookResult {
	return agent.ToolCallHookResult{}
}

// releaseDocsLeases does nothing: this build has no documentation bundle to lease.
func releaseDocsLeases() {}

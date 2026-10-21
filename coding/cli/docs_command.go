//go:build !pig_strip_docs

package cli

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/configroot"
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

// prepareDocsBundle records whether the system prompt's docs section may name the documentation bundle and defers the write of a bundle that is
// not on disk yet until something other than PiG can read it ([writeDocsBundle]). A Piglet that strips docs names and writes none.
func prepareDocsBundle() {
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.Docs) {
		return
	}
	present, pending, err := pigdocs.Prepare(codingagent.ConfigRoot())
	// pig additive (D22): a config root this user cannot write leaves no bundle to name; `pig docs show` still reads the embedded one.
	configroot.SetDocsPresent(err == nil && present)
	if err == nil && present && !pending {
		// pig additive (D22): the session reads the pages its prompt names for as long as it runs, so another build's eviction must leave them.
		holdDocsBundle()
	}
	if err == nil && present && pending {
		configroot.SetDocsWriter(sync.OnceFunc(func() {
			synced := pigdocs.EnsureSynced(codingagent.ConfigRoot()) == nil
			configroot.SetDocsPresent(synced)
			if synced {
				holdDocsBundle()
			}
		}))
	}
}

// docsLeases are the releases of the leases this process holds on its documentation bundle. A session holds them until it exits: the exit path releases them just before it writes its profiles, so the descriptor count the exit record reports is the process's own, and the operating system would release them with the process anyway. Tests release them too.
var docsLeases struct {
	sync.Mutex
	releases []func()
}

// holdDocsBundle leases this build's bundle for the life of the process ([pigdocs.HoldBundle]).
func holdDocsBundle() {
	release := pigdocs.HoldBundle(codingagent.ConfigRoot())
	docsLeases.Lock()
	docsLeases.releases = append(docsLeases.releases, release)
	docsLeases.Unlock()
}

// releaseDocsLeases gives back the leases this process took on its documentation bundle. The exit paths call it just before the profiles are written, and a
// test calls it so the test's temporary directory can be removed where an open lease file blocks that (Windows). It is idempotent.
func releaseDocsLeases() {
	docsLeases.Lock()
	releases := docsLeases.releases
	docsLeases.releases = nil
	docsLeases.Unlock()
	for _, release := range releases {
		release()
	}
}

// writeDocsBundle writes the bundle prepareDocsBundle deferred, before a reader other than PiG can follow the paths the system prompt names: a
// tool call, an extension process, the interactive and RPC modes. It returns at once when nothing is deferred.
func writeDocsBundle() { configroot.WriteDocs() }

// writeDocsBundleBeforeToolCall is a tool hook that writes the bundle prepareDocsBundle deferred before the first tool runs, and lets every call
// proceed unchanged.
func writeDocsBundleBeforeToolCall(context.Context, string, string, json.RawMessage) agent.ToolCallHookResult {
	writeDocsBundle()
	return agent.ToolCallHookResult{}
}

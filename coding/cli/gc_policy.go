// SPDX-License-Identifier: MIT

package cli

import "github.com/MichaelKinsy/PiG/internal/gcpolicy"

// gcPolicyFor is the GC policy of a run: print, json and rpc runs use D175's, an interactive run D621's, and a --listen server none (D232).
func gcPolicyFor(mode appMode, listen string) (gcpolicy.Policy, bool) {
	if listen != "" {
		return gcpolicy.Policy{}, false
	}
	switch mode {
	case appModePrint, appModeJSON, appModeRPC:
		return gcpolicy.Batch, true
	case appModeInteractive:
		// pig additive (D621): an interactive run collects at GOGC 150 until 8 MiB are live.
		return gcpolicy.Interactive, true
	}
	return gcpolicy.Policy{}, false
}

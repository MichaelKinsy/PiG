// SPDX-License-Identifier: MIT

package cli

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/gcpolicy"
)

// D175, D621, D232: print, json and rpc runs take the batch GC policy, an interactive run the interactive one, and a --listen server of any
// mode none.
func TestGCPolicyFollowsTheRunMode(t *testing.T) {
	for _, tc := range []struct {
		mode   appMode
		listen string
		want   gcpolicy.Policy
		ok     bool
	}{
		{appModePrint, "", gcpolicy.Batch, true},
		{appModeJSON, "", gcpolicy.Batch, true},
		{appModeRPC, "", gcpolicy.Batch, true},
		{appModeInteractive, "", gcpolicy.Interactive, true},
		{appModeRPC, "127.0.0.1:0", gcpolicy.Policy{}, false},
		{appModeInteractive, "127.0.0.1:0", gcpolicy.Policy{}, false},
	} {
		got, ok := gcPolicyFor(tc.mode, tc.listen)
		if ok != tc.ok || got != tc.want {
			t.Errorf("gcPolicyFor(%q, %q) = %+v, %v; want %+v, %v", tc.mode, tc.listen, got, ok, tc.want, tc.ok)
		}
	}
}

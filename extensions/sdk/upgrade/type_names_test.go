// SPDX-License-Identifier: MIT

package upgrade_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// The types that read no types are declared in an internal package named upgrade, so what fmt and a JSON decode error
// print for them is what they printed when this package declared them. The pig command prints such a decode error for a
// driver reply it cannot read.
func TestPlanTypesKeepTheirNames(t *testing.T) {
	for want, value := range map[string]any{
		"upgrade.Change":      upgrade.Change{},
		"upgrade.Rule":        upgrade.Rule{},
		"upgrade.Diagnostic":  upgrade.Diagnostic{},
		"upgrade.Drift":       upgrade.Drift{},
		"upgrade.FileSource":  upgrade.FileSource(nil),
		"upgrade.LoadOptions": upgrade.LoadOptions{},
		"upgrade.Rewrite":     upgrade.Rewrite{},
		"upgrade.FileChange":  upgrade.FileChange{},
		"upgrade.Skipped":     upgrade.Skipped{},
		"*upgrade.Result":     &upgrade.Result{},
		"upgrade.Reply":       upgrade.Reply{},
	} {
		if got := fmt.Sprintf("%T", value); got != want {
			t.Errorf("%%T = %s, want %s", got, want)
		}
	}
	var reply upgrade.Reply
	err := json.Unmarshal([]byte(`{"Result":5}`), &reply)
	if want := "json: cannot unmarshal number into Go struct field Reply.Result of type upgrade.Result"; err == nil || err.Error() != want {
		t.Errorf("decoding a number as the plan: %v, want %s", err, want)
	}
}

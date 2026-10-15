//go:build pig_strip_self_update

package cli

import (
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// pig additive (D92): this Piglet Binary compiled out self-update; its Piglet distribution updates it.
func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.SelfUpdate) }

// runSelfUpdate refuses: this build cannot replace itself. `pig update --all`
// still updates packages first and then exits 1 with this message.
func runSelfUpdate(bool) int {
	return reportStrippedFeature("self-update", pigstrip.SelfUpdate, selfUpdateStrippedHint)
}

// binaryUpdateChecker is nil: this build shows no new-version notice.
func binaryUpdateChecker() func() *codingagent.BinaryUpdate { return nil }

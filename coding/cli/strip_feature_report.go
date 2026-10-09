package cli

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// selfUpdateStrippedHint follows the stripped self-update message: a Piglet
// Binary is updated by whatever distributes it.
const selfUpdateStrippedHint = "; update it through its Piglet distribution"

// reportStrippedFeature prints that the user requested a feature this Piglet
// strips, followed by hint, and returns the exit code 1.
// pig additive (D92): one message for a stripped feature's CLI entry point, compiled out or stripped at runtime.
func reportStrippedFeature(what, id, hint string) int {
	fmt.Fprintf(os.Stderr, "pig: %v%s\n", pigstrip.Error(what, pigstrip.ListFeatures, id), hint)
	return 1
}

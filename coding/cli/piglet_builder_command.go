//go:build !pig_strip_piglet_builder

package cli

import (
	"os"

	"github.com/MichaelKinsy/PiG/coding/pigletbuild"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// This file and its coding/pigletbuild import are the boundary a Piglet
// Binary compiles out with the pig_strip_piglet_builder tag.

// runPigletBuilderCommand runs `pig piglet build` and `pig piglet publish`
// and returns the exit code, or -1 when args target another command.
func runPigletBuilderCommand(args []string) int {
	if len(args) < 2 || args[0] != "piglet" || (args[1] != "build" && args[1] != "publish") {
		return -1
	}
	// pig additive (D92): a Piglet that strips the builder reports it absent, as its Binary does.
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.PigletBuilder) {
		return reportStrippedFeature("pig piglet "+args[1], pigstrip.PigletBuilder, "; use stock pig")
	}
	if args[1] == "build" {
		return pigletbuild.RunPigletBuildCommand(args[2:], os.Stdout, os.Stderr)
	}
	return pigletbuild.RunPigletPublishCommand(args[2:], os.Stdout, os.Stderr)
}

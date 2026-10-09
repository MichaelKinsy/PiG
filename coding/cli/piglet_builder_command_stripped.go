//go:build pig_strip_piglet_builder

package cli

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// pig additive (D92): this Piglet Binary compiled out the Piglet builder (coding/pigletbuild).
func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.PigletBuilder) }

// runPigletBuilderCommand reports that `pig piglet build` and `pig piglet publish` are unavailable in this build.
func runPigletBuilderCommand(args []string) int {
	if len(args) < 2 || args[0] != "piglet" || (args[1] != "build" && args[1] != "publish") {
		return -1
	}
	return reportStrippedFeature("pig piglet "+args[1], pigstrip.PigletBuilder, "; use stock pig")
}

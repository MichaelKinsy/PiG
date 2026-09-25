package codingagent

import (
	"os"
	"strings"
)

// experimentalFeaturesEnabled reports whether experimental features are on.
// Upstream core/experimental.ts areExperimentalFeaturesEnabled checks
// process.env.PI_EXPERIMENTAL === "1". PI_EXPERIMENTAL mirrors Pi exactly (strict
// "1"); PIG_EXPERIMENTAL is Pig's product-neutral alias, accepting the same
// truthy spellings as PIG_OFFLINE (see updateChecksOffline).
func experimentalFeaturesEnabled() bool {
	if os.Getenv("PI_EXPERIMENTAL") == "1" {
		return true
	}
	value := strings.ToLower(strings.TrimSpace(os.Getenv("PIG_EXPERIMENTAL")))
	return value == "1" || value == "true" || value == "yes"
}

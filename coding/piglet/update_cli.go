package piglet

import (
	"context"
	"fmt"
	"io"
	"slices"

	pigletrelease "github.com/MichaelKinsy/PiG/coding/piglet/release"
)

const updateUsage = "Usage: pig piglet update <name> [--version version] [--target os/arch] [--accept-signer key-id] [--json] [--no-input]"

// pig additive (D18): named Binary updates use the installed signed repository and tag namespace, never the repository's latest release.
func cmdUpdate(args []string, stdout, stderr io.Writer) int {
	jsonMode := slices.Contains(args, "--json")
	name, target, version, accepted, err := parseReleaseArgs(args, "update")
	if err != nil {
		if jsonMode {
			writePigletCommandJSON(pigletCommandOutput{Command: "update", Error: err.Error()}, stdout)
		} else {
			_, _ = fmt.Fprintf(stderr, "error: %v\n%s\n", err, updateUsage)
		}
		return 2
	}
	if name == "" {
		_, _ = fmt.Fprintln(stdout, updateUsage)
		return 0
	}
	if DistributionOffline() {
		err = fmt.Errorf("cannot update Piglet while offline; unset PIG_OFFLINE and PI_OFFLINE")
	}
	var result pigletrelease.Result
	if err == nil {
		result, err = pigletrelease.Update(context.Background(), name, pigletrelease.Options{Target: target, Version: version, AcceptSigner: accepted})
	}
	if err != nil {
		if jsonMode {
			writePigletCommandJSON(pigletCommandOutput{Command: "update", Error: err.Error()}, stdout)
		} else {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		}
		return 1
	}
	message := fmt.Sprintf("Current %s %s for %s, signed by %s\n  binary: %s\n  receipt: %s", result.Piglet, result.Version, result.Target, result.SignerKeyID, result.Artifact, result.Receipt)
	if jsonMode {
		writePigletCommandJSON(pigletCommandOutput{Command: "update", Success: true, Output: message}, stdout)
	} else {
		_, _ = fmt.Fprintln(stdout, message)
	}
	return 0
}

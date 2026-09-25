package piglet

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	pigletrelease "github.com/MichaelKinsy/PiG/coding/piglet/release"
)

const pullUsage = "Usage: pig piglet pull <release-index-url|github:owner/repo@version> [--target os/arch] [--version version] [--accept-signer key-id] [--json] [--no-input]"

// pig additive (D18): pull is the stock, product-neutral installer for signed
// Piglet Binary release assets.
func cmdPull(args []string, stdout, stderr io.Writer) int {
	jsonMode := slices.Contains(args, "--json")
	ref, target, version, accepted, parseErr := parsePullArgs(args)
	if parseErr != nil {
		if jsonMode {
			writePigletCommandJSON(pigletCommandOutput{Command: "pull", Success: false, Error: parseErr.Error()}, stdout)
		} else {
			_, _ = fmt.Fprintf(stderr, "error: %v\n%s\n", parseErr, pullUsage)
		}
		return 2
	}
	if ref == "" {
		_, _ = fmt.Fprintln(stdout, pullUsage)
		return 0
	}
	result, err := pigletrelease.Pull(context.Background(), ref, pigletrelease.Options{
		Target: target, Version: version, AcceptSigner: accepted,
	})
	if err != nil {
		if jsonMode {
			writePigletCommandJSON(pigletCommandOutput{Command: "pull", Success: false, Error: err.Error()}, stdout)
		} else {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		}
		return 1
	}
	message := fmt.Sprintf("Pulled %s %s for %s, signed by %s\n  binary: %s\n  receipt: %s",
		result.Piglet, result.Version, result.Target, result.SignerKeyID, result.Artifact, result.Receipt)
	if jsonMode {
		writePigletCommandJSON(pigletCommandOutput{Command: "pull", Success: true, Output: message}, stdout)
	} else {
		_, _ = fmt.Fprintln(stdout, message)
	}
	return 0
}

func parsePullArgs(args []string) (ref, target, version, accepted string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json", "--no-input":
		case "-h", "--help":
			if len(args) != 1 {
				return "", "", "", "", fmt.Errorf("--help cannot be combined with other arguments")
			}
			return "", "", "", "", nil
		case "--target", "--version", "--accept-signer":
			if i+1 >= len(args) {
				return "", "", "", "", fmt.Errorf("%s requires a value", args[i])
			}
			flag, value := args[i], args[i+1]
			i++
			if value == "" || strings.HasPrefix(value, "-") {
				return "", "", "", "", fmt.Errorf("%s requires a value", flag)
			}
			switch flag {
			case "--target":
				target = value
			case "--version":
				version = value
			case "--accept-signer":
				accepted = value
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", "", "", fmt.Errorf("unknown option %q", args[i])
			}
			if ref != "" {
				return "", "", "", "", fmt.Errorf("pull accepts one release reference")
			}
			ref = args[i]
		}
	}
	if ref == "" {
		return "", "", "", "", fmt.Errorf("a release reference is required")
	}
	return ref, target, version, accepted, nil
}

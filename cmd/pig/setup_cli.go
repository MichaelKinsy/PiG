package main

import "os"

// setupCli mirrors upstream packages/coding-agent/src/cli/setup.ts, which the
// CLI and RPC entry points run before main. It sets the process markers child
// processes inherit. The rest of upstream setupCli has no Go counterpart:
// process.title is the executable name, Node warnings do not exist, and the
// HTTP transport is configured from settings in main.
func setupCli() {
	_ = os.Setenv("PI_CODING_AGENT", "true") // Setenv fails only for invalid names.
	_ = os.Setenv("AI_AGENT", "pi")
}

// pig divergence (D2): refuse the `pi` name to protect Pi's command identity.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// guardBinaryIdentity rejects Pi's binary name and reports suspect install paths.
func guardBinaryIdentity() string {
	argv0 := os.Args[0]
	resolved, err := os.Executable()
	if err != nil {
		resolved = argv0
	}
	resolved, _ = filepath.EvalSymlinks(resolved)

	base := filepath.Base(resolved)
	if base == "pi" || base == "pi.exe" {
		fmt.Fprintf(os.Stderr, `pig: refusing to run as %q.

This binary is PiG (the Go port of pi-coding-agent), not upstream pi.
Running it as "pi" would silently shadow the real pi binary and corrupt
shared expectations (PATH, sessions, ~/.pi/ tooling, shell aliases).

Resolved path: %s

If you want pig: rename or symlink it back to "pig".
If you want pi:  install via npm/mise; do not point "pi" at this file.
`, base, resolved)
		os.Exit(2)
	}

	// Warn when the binary is installed inside Pi's npm package.
	if strings.Contains(resolved, "/npm-mariozechner-pi-coding-agent/") ||
		strings.Contains(resolved, "/@mariozechner/pi-coding-agent/") ||
		strings.Contains(resolved, "/npm-earendil-works-pi-coding-agent/") ||
		strings.Contains(resolved, "/@earendil-works/pi-coding-agent/") {
		fmt.Fprintf(os.Stderr, "pig: warning: running from upstream pi install path: %s\n", resolved)
	}
	return resolved
}

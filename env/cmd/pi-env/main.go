// Command pi-env is the remote execution environment daemon of Pi Durable: it serves the pi-env protocol on stdin and
// stdout. It is started as `pi-env serve --token <hex>`, usually through ssh.
//
// Ports packages/env/daemon/src/main.rs
//
// pig divergence (D96): the daemon is this Go program, with Pi's command line and wire protocol, not Pi's Rust binary.
package main

import (
	"fmt"
	"os"
	"slices"

	"github.com/MichaelKinsy/PiG/env/daemon"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

const usage = "usage: pi-env serve --token <hex>"

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin *os.File, stdout, stderr *os.File) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		index := slices.Index(args, "--token")
		if index < 0 || index+1 >= len(args) {
			_, _ = fmt.Fprintln(stderr, usage)
			return 2
		}
		if err := daemon.Serve(stdin, stdout, args[index+1], daemon.Options{Version: pigversion.UpstreamVersion}); err != nil {
			_, _ = fmt.Fprintf(stderr, "pi-env: %v\n", err)
			return 1
		}
		return 0
	case "--version":
		_, _ = fmt.Fprintf(stdout, "pi-env %s\n", pigversion.UpstreamVersion)
		return 0
	}
	_, _ = fmt.Fprintln(stderr, usage)
	return 2
}

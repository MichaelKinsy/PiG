// SPDX-License-Identifier: MIT

// Command sessionscale writes and measures long-lived session fixtures (see package sessionscale).
//
//	sessionscale gen -out DIR [-bytes 299M] [-context 150000] [-window 0] [-seed 1]
//	sessionscale serve -addr 127.0.0.1:0
//	sessionscale k8s seed|sample [flags]   bench/k8s's adapter: one fixture, or one k8sbench sample line
//	sessionscale run -arm NAME -cmd 'BIN ARGS' -fixture DIR [-mode interactive|rpc] [-prompts 0,5,50] [-soak 6m] -out FILE.jsonl
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: sessionscale gen|run|serve|k8s [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "gen":
		err = gen(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	case "serve":
		err = serve(os.Args[2:])
	case "k8s":
		err = k8s(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sessionscale:", err)
		os.Exit(1)
	}
}

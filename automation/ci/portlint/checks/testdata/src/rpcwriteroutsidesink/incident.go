package rpcwriteroutsidesink

import (
	"fmt"
	"os"
)

// The B4 shape: a command's response waits behind the microtask queue before the RPC writer sends it, while extension output (a requestless ui.notify, a timer callback, a session event) is written straight to stdout. The output overtakes the response.

type rpcResponse struct{ id string }

type responseQueue struct{ pending []rpcResponse }

func (q *responseQueue) complete(r rpcResponse) { q.pending = append(q.pending, r) }

func forwardExtensionOutput(line string) {
	fmt.Fprintln(os.Stdout, line) // want `os.Stdout is written outside the RPC writer`
}

// SPDX-License-Identifier: MIT

// Command driver type checks a Go extension against the SDK and plans the rewrites of [upgrade.Session.Plan].
//
// The pig command does not link the type checker (go/types and the importers are about 1 MiB of code that only this analysis runs). It builds this program from the staged SDK with the go command it already uses for extensions, and runs it.
//
// The driver reads one JSON [upgrade.LoadOptions] from standard input and writes one JSON [upgrade.Reply] to standard output. It keeps reading standard input: when the caller closes it, the analysis is cancelled, which is how a cancelled caller stops the go command the driver started.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// pig divergence (D272): this program is the upgrade type check, built and run by the pig command from the staged SDK so that the pig binary does not link go/types.
func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "driver:", err)
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer) error {
	var options upgrade.LoadOptions
	if err := json.NewDecoder(in).Decode(&options); err != nil {
		return fmt.Errorf("read the request: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, in)
		cancel()
	}()
	var reply upgrade.Reply
	if session, err := upgrade.Open(ctx, options); err != nil {
		reply.Error = err.Error()
	} else if reply.Result, err = session.Plan(); err != nil {
		reply.Result, reply.Error = nil, err.Error()
	}
	return json.NewEncoder(out).Encode(reply)
}

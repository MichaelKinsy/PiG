// Command main runs the durable coding agent: pi's coding tools on the pi-durable Harness, with its own TUI.
//
//	go run ./internal/experimental/durableagent/main [--continue]
package main

// Ports packages/coding-agent/src/experimental/durable/main.ts

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/internal/experimental"
	"github.com/MichaelKinsy/PiG/internal/experimental/durableagent"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) (err error) {
	options, err := durableagent.ParseArgs(args)
	if err != nil {
		return err
	}
	session, err := durableagent.Open(ctx, options, durableagent.CodingProfile)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, session.Close()) }()
	return experimental.RunDurableTui(ctx, session.View, session.Controller, session.Settings)
}

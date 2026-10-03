// Command main runs the durable vacation planner: a research subagent that reports back, on the pi-durable Harness, with the durable agent's TUI.
//
//	go run ./internal/experimental/vacation/main [--continue]
package main

// Ports packages/coding-agent/src/experimental/vacation/main.ts

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/internal/experimental"
	"github.com/MichaelKinsy/PiG/internal/experimental/durableagent"
	"github.com/MichaelKinsy/PiG/internal/experimental/vacation"
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
	session, err := vacation.Open(ctx, options)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, session.Close()) }()
	return experimental.RunDurableTui(ctx, session.View, session.Controller, session.Settings)
}

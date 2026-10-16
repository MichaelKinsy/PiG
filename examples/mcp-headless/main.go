// Command mcp-headless connects to one MCP server over stdio, prints the
// tools it offers, and exits. It uses only package mcp, so it links no
// terminal UI.
//
// Run from the repo root:
//
//	go run ./examples/mcp-headless <command> [args...]
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mcp-headless <command> [args...]")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run lists the tools of the MCP server the command starts. It returns instead of exiting, so the deferred Close runs.
func run(command string, args []string) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "mcp-headless", Version: "0.0.1"}})
	defer func() {
		if closeErr := client.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close: %w", closeErr)
		}
	}()
	if _, err := client.Connect(ctx, mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: command, Args: args})); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	tools, err := client.ListTools(ctx, mcp.RequestOptions{})
	if err != nil {
		return fmt.Errorf("list tools: %w", err)
	}
	for _, tool := range tools {
		fmt.Printf("%s\t%s\n", tool.Name, tool.Description)
	}
	return nil
}

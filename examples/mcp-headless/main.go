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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "mcp-headless", Version: "0.0.1"}})
	defer client.Close()
	if _, err := client.Connect(ctx, mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: os.Args[1], Args: os.Args[2:]})); err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	tools, err := client.ListTools(ctx, mcp.RequestOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "list tools:", err)
		os.Exit(1)
	}
	for _, tool := range tools {
		fmt.Printf("%s\t%s\n", tool.Name, tool.Description)
	}
}

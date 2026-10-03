//go:build pig_strip_mcp

package main

// runMcpCommand is absent from a build without built-in MCP: `mcp` is then an ordinary argument.
func runMcpCommand([]string) int { return -1 }

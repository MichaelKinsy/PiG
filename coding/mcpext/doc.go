// Package mcpext is the built-in MCP integration (`builtin:mcp`).
//
// It connects the servers from `mcp.json` and the servers extensions register
// with registerMcpServer, and registers their tools as `mcp__<server>__<tool>`.
// The package depends on the MCP client (package mcp) and on a small
// [Host] interface, not on the extension runner, so a Piglet can leave the
// package out of its build or disable it at startup without touching any
// other code: one registration seam links it, and nothing else imports it.
//
// The `/mcp` command and its terminal manager view are registered by [Factory].
// The `pi mcp` CLI is wired by the CLI family; this package exposes what it
// needs: the connections, the server list and its change notifications, sign-in
// and sign-out, reconnect, enable and exposure changes, and the config editors.
package mcpext

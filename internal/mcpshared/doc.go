// Package mcpshared holds the dependency-light parts of package mcp that applications use without the MCP client: the tool and namespace naming and the
// validation of `mcpServers` entries. Package mcp re-exports them, so a build that strips the MCP client (pig_strip_mcp) still links coding/extension's
// use of them.
package mcpshared

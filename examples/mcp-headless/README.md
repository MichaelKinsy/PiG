# mcp-headless

Connects to an MCP server over stdio and lists its tools, using only package `mcp`.
It links no terminal UI and no word-segmentation dictionaries.

    go run ./examples/mcp-headless npx -y @modelcontextprotocol/server-everything

## Binary size

Stripped, `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`, darwin/arm64, Go 1.27.2:

| Program | Imports | Size |
|---|---|---|
| `examples/mcp-headless` | `mcp` | 5,302,450 bytes (5.1 MiB) |
| `examples/mcp-headless-full` | `coding/mcpext` (via `coding/extension`, which links `tui`) | 19,382,754 bytes (18.5 MiB) |

`coding/mcpext` no longer imports `tui` itself, but it still reaches it through
`coding/extension`, so a program that needs only MCP connections should use
package `mcp` directly until `coding/extension` is split from `tui`
(`docs/design/extension-api-tui-split.md`).
`go list -deps ./examples/mcp-headless` contains no `tui` or `wordsegmenter` package; a test
(`coding/mcpext/headless_deps_test.go`) enforces that for `mcp` and `mcp/oauth`.

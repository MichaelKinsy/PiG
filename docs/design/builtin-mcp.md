# Built-in MCP (`builtin:mcp`)

This document records the boundary of PiG's native Model Context Protocol support and the plan to retire `pi-mcp-adapter` in its favor. The reference behavior is the MCP client and the built-in MCP extension of upstream 0.99.1: `packages/mcp` and `packages/coding-agent/src/extensions/mcp`.

## Packages

| Go package | Upstream | Contents |
|---|---|---|
| `mcp` | `packages/mcp/src` | `Client`, `Transport`, `StdioTransport`, `StreamableHTTPTransport`, `ConsumeSSEStream`, `ToLLMContent` |
| `mcp/mcptest` | `packages/mcp/src/testing` | in-memory transport pair |
| `mcp/oauth` | `packages/mcp/src/oauth` | discovery, PKCE authorization code flow, dynamic client registration, refresh, `McpOAuthProvider`, `OAuthCallbackServer` |
| `coding/mcpext` | `packages/coding-agent/src/extensions/mcp` | `mcp.json` loading and editing, server connections, tool and resource tools, OAuth credentials and sign-in, the extension's event handlers |
| `coding/extension` (`mcp_servers.go`) | `core/mcp-servers.ts` | server config types and validation, exposure resolution, `McpServerRegistry` |

The `mcp` and `mcp/oauth` packages depend on nothing else in PiG. `coding/mcpext` reaches the runner through a five-method `Host` interface and receives events as method calls, so it never imports the runner or the extension host.

## Boundary for Piglets

A Piglet must be able to leave MCP out (issue #92). The boundary is one seam:

- Nothing outside `coding/mcpext` imports it, except the registration seam `coding/extension/builtin/mcp_on.go`. It lists the replaceable built-in `mcp` after `codemode` and `tool-search`, as Pi's `extensions/index.ts` does, and builds the extension with `mcpext.NewBuiltin`, the only call to `mcpext.Factory`. `cmd/pig/builtin_extensions.go` configures it (`ConfigureMcp`: agent directory, config directory name, browser opener).
- The registration file carries `//go:build !pig_strip_mcp`. A stripped build links `mcp_off.go`, which registers nothing, and `mcp`, `mcp/oauth` and `coding/mcpext` leave the binary (no `coding/mcpext` symbol remains).
- Runtime disable is not calling `Factory`. `--no-extensions` and `-e builtin:mcp` selection follow upstream's `builtin:` naming, which the resource loader family owns.
- A Piglet's tool scope keeps working: MCP tools are ordinary tools whose names start with `mcp__<server>__` and whose namespace is `mcp__<server>`.

The seam file and the generated inventory entry belong to the runner family. This lane does not add feature checks inside any ported algorithm.

## Behavior that differs from Pi, by mechanism

These follow from Go, not from a decision to differ:

- `killProcessTree` uses the process group on Unix and `taskkill` on Windows, as upstream does. Go has no process `exit` hook, so a host that exits without closing its transports calls `mcp.KillLiveProcessGroups()`.
- Tool arguments and schemas stay `json.RawMessage` (or an ordered object) so their key order reaches the server and the provider unchanged.
- A `Map`-like object with insertion order (`headers`, `env`, `toolExposure`) is an ordered type, because a Go map has no order. Environment variables of a stdio server are applied in sorted key order.
- Results of `tools/call` pass to codemode scripts as the server sent them (`CallToolResult.Raw`), without `_meta`.
- The client name sent to servers is `pig`, not `pi`, and the OAuth `client_name` is `pig`. Upstream sends `pi`.

## Session wiring

`builtin:mcp` loads through the one extension loader every mode shares (`cmd/pig/extension_set.go`), so print, JSON, RPC and interactive modes get it from the same list. The pieces that make it work in a Session, each proven by the real binary tests in `cmd/pig/mcp_builtin_binary_test.go`:

- `Context.RefreshTools` and `Context.GetMcpServers` (`agent-session.ts` `_bindExtensionCore`): a tool registered after load is admitted and declared; the extension reads the servers other extensions registered. Interactive mode answers `getAllTools`, `setActiveTools` and `refreshTools` from the Session's registry and loadout, as the other modes do.
- `Runner.ReportUnhandledMcpServers` runs after `session_start` in every bind and after a reload (`agent-session.ts` `bindExtensions`, `reload`).
- The Session's runner shares the extension host's registration runtime (`RuntimeOptions.ExtensionRuntime`), so a server a Node or Rust extension registers reaches `mcp_servers_change`.
- Calls to one server keep call order: `ToolDefinition.ReserveCallOrder`, a lane per server, a codemode wait on `AwaitsInitiation` tools, and `mcp.Client` writing requests in id order through `OrderedSender` transports. Pi's synchronous prefix gives this order for free; Go goroutines do not.

## Retiring `pi-mcp-adapter`

`pi-mcp-adapter` is a Pi extension that PiG runs in its Node extension runtime, and Piglets scope its tools through the `pig-mcp-adapter` extension name and the `mcp:<server>` source convention (`coding/piglet/scope.go`, additive feature D23). `builtin:mcp` replaces it. The plan, in order:

1. Wire `builtin:mcp` (runner family): the registration seam, `registerMcpServer` and `mcp_servers_change`, tool exposure and nested tool calls in the session, and the `/mcp` command and `pi mcp` CLI on top of `mcpext.Extension`.
2. Teach Piglet scoping the native tools: `ScopeTools` treats a tool in namespace `mcp__<server>` as an MCP tool of `<server>`, exactly as it treats a `mcp:<server>` source today. `extensions: [pig-mcp-adapter]` keeps enabling MCP tools until step 4.
3. Migrate configuration. `mcp.json` in the agent directory and `<project>/.pig/mcp.json` use the `mcpServers` shape that upstream and other MCP clients share, so a working entry moves over as is. Check the adapter's own configuration location and copy its server entries into the native file. Native servers default to `"exposure": "codemode"`; set `"exposure": "direct"` to declare a server's tools to the model the way the adapter did.
4. Remove the adapter from the Standard Piglet's package set and from the docs, keep the `pig-mcp-adapter` scope name as an alias for one release, then delete the alias with a changelog entry.

Do not delete the adapter's scoping code until step 4. Nothing in this lane deletes it.

## Deferred to other families

- The `/mcp` slash command, its terminal manager (`ui.ts`) and the `pi mcp` CLI (`cli.ts`).
- Generic tool-call rendering of MCP tools (`renderCall`, `renderResult`).
- The session-level case "rejects names another extension registered" of `agent-session-mcp.test.ts`: Pi throws the clash inside the other extension's factory; PiG queues a registration made while a factory runs (`docs/extension-api-parity.md`, `registerMcpServer`), so the clash fails that extension's load. The runtime checks are ported in `coding/extension/host/inproc/mcp_servers_registration_test.go`.

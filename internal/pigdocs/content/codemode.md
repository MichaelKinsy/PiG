# Codemode and tool search

`codemode` lets the model write a short JavaScript program that calls PiG's other tools, instead of calling them one at a time. Only the script's output reaches the model. `tool_search` finds tools that are not declared to the model and declares the matches for the next call.

The scripts run in [QuickJS](https://bellard.org/quickjs/) compiled to WebAssembly and executed by [wazero](https://wazero.io) inside the `pig` process. PiG needs no Node.js for codemode, and a script can reach only the tools you give it.

PiG ships two built-in extensions that add tools. Both are written in Go and are off by default. The `mcp` extension turns them on when an MCP server needs them (see [MCP](mcp.md#control-tool-exposure)). To enable them yourself, name them in `--tools` or `defaultTools`.

| Tool | Purpose |
|---|---|
| `codemode` | Run JavaScript that calls the other tools, for example in parallel with `Promise.allSettled`; only the script's output reaches the model |
| `tool_search` | Search tools that are not declared to the model (`codemode` and `deferred` exposure, such as MCP tools) and declare the matches for the next call |

## Enable codemode

To turn on `codemode` for every session, add it to the default tools in `~/.pig/agent/settings.json` or a project's `.pig/settings.json`:

```json
{
  "defaultTools": ["+codemode"]
}
```

This keeps `read`, `bash`, `edit`, and `write` and adds `codemode`. For one invocation, list every tool, since `--tools` replaces the selection:

```sh
pig --tools read,bash,edit,write,codemode
```

Codemode is useful without MCP: scripts can run several tool calls in parallel, filter large output before it reaches the model, and call classifier models through the `models` helper (see below).

## How codemode works

Codemode scripts run in a QuickJS sandbox that can only reach the other tools, through `tools.<name>(args)`; `ALL_TOOLS` lists them. Output comes from `text(value)`, `image(dataUrlOrImageContent)`, `console.*`, and a top-level `return value`; `exit()` ends the script early. The result starts with `Script completed` or `Script failed`, the wall time, and the output; a failed script keeps its partial output, followed by `Script error:` and the error.

A script may start with an options line such as `// @options: {"max_output_tokens": 2000, "timeout_ms": 60000}`. `max_output_tokens` (default 10000) limits the output: longer output keeps its start and end, and the full text is written to a temp file whose path is included in the result. `timeout_ms` is a hard deadline, unset by default.

While `codemode` is active, `codemode.mode` in [settings](settings.md#tools) decides how the other tools are presented. With `on` (default) declared tools keep being declared and their descriptions show how to call them from scripts. With `only` they are hidden from the model and listed in the `codemode` description instead, so the model calls them through scripts.

The `codemode` description lists the callable tools with their TypeScript declarations, grouped by namespace (for example one MCP server). Tools with `deferred` exposure, which includes MCP tools with the default `codemode` exposure, are not listed and do not affect the description, so it stays the same while MCP servers connect. Declarations share a budget of 3000 estimated tokens (`codemode.inlineBudget` in [settings](settings.md#tools)). Scripts find the rest with `await searchTools(query, { limit, namespace })`, which ranks tools with BM25, and `await describeTool(name)`, or by filtering `ALL_TOOLS`. `await describeNamespace(name)` returns a namespace's description, its instructions (for MCP servers, the server instructions), and the names of its tools.

Tools with an output schema resolve to structured values: `bash` to `{ output, truncated, full_output_path?, exit_code, wall_time_seconds }`, also for non-zero exit codes, and MCP tools to their `CallToolResult`. Other tools resolve to their text output. The `output` of `bash` is not limited to the 2000 lines or 50KB the model sees: it holds up to 1 MiB, and longer output keeps its first and last 512 KiB around an omission marker, with `truncated` set and the full output in `full_output_path`.

`store(key, value)` and `load(key)` keep JSON values across `codemode` calls: each successful script that stores values appends a `codemode-store` custom entry to the session, so resumed sessions keep the values and each branch sees only the values written on its path. Scripts can also use `models`: `getModelsOfType`, `getAvailableOfType`, and `getModelOfType` list the model catalog, and `classify(model, context)` runs a classifier model with the session's credentials, at most four at a time per script.

## Tool search

`tool_search` is off by default; enable it with `"defaultTools": ["+tool_search"]` or `--tools`. It uses the same ranking as `searchTools()` over tools that are not declared yet and declares the matches for the next model call. Loaded tools are recorded in the session like other tool changes, so they stay declared on that branch.

## Where the settings live

`defaultTools` and `codemode` are in [settings](settings.md#tools). `--no-extensions` disables both built-in extensions; load one for a single run with `-e builtin:codemode` or `-e builtin:tool-search`. An extension that registers a tool named `codemode` or `tool_search` replaces the built-in tool with a warning.

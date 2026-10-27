### Upgrading

- Go API `coding/extension/host/subprocess`: editor nodes of an extension's view need `subprocess.EnableEditorViews()`, which the `pig` command calls where it creates the extension host. A program that serves extension views to its own terminal calls it once before the first view is built; until then a view that holds an editor fails with an error that names the call. A program that draws no extension editor needs no change and no longer links the terminal editor.

### Added

- `examples/headless` is a complete small embed: the session API, MCP servers from `mcp.json` and one provider (Anthropic), no terminal. Its test runs a prompt against a fake provider and a stdio MCP server and checks that the server's tool reaches the provider and is called.
- The Go SDK page has a section on headless embedding: the `pig_strip_*` build tags an embedder can pass to leave out features it does not use (the Node runtime, the AWS SDK, highlighting grammars and others), with the size each one saves, and how to use `mcpext` without a terminal.
- `TestHeadlessEmbedBinarySizeGate` holds the size of `examples/headless`, plain and with every strip tag except `pig_strip_mcp`, on each release platform to a ceiling in `automation/perf/ratchet.toml` (`binary.headless.*`), the way `binary.pig.*` holds the `pig` command.

### Changed

- A program that embeds PiG without a terminal is 7.1 MB smaller (47.2 MB to 40.1 MB for `examples/headless` on linux/amd64): the terminal editor, the word-segmentation dictionaries behind it (6.3 MB) and the interactive mode's announcement image (0.5 MB) are no longer linked into a program that never draws an editor. The `pig` command links them as before; its size is unchanged within one page (69,812,384 to 69,816,480 bytes on linux/amd64).

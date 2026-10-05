### Changed

- PiG follows Pi 1.0.1. `pig --version` prints `<PiG release>+1.0.1`.
- Anthropic requests that define tools after the first turn send them inline as tool definition blocks with the `inline-tools-2026-09-15` beta, as Pi 1.0.1 does.
- Bedrock adaptive thinking on Claude Opus 4.7, Opus 4.8, Opus 5, Sonnet 5 and Fable 5 drops thinking blocks whose prefix no longer matches (`block_binding`), outside GovCloud.
- The model catalog follows Pi 1.0.1: Bedrock prices above 272k input tokens, Cloudflare AI Gateway model IDs with dashes, the Together DeepSeek V4 Pro rename, Cloudflare's Clef classifiers, and NVIDIA's default model `nvidia/nemotron-3-ultra-550b-a55b`.
- Anthropic workload identity federation joins the request's betas and the OAuth beta with `,` and identifies as `Anthropic/JS 0.129.0`, as the SDK Pi 1.0.1 pins does.
- Sign-in URLs end their terminal hyperlinks with ST instead of BEL.

### Added

- Project overrides for MCP servers: a `.pi/mcp.json` entry without `command`, `url` or `type` changes only `enabled`, `exposure` and `toolExposure` of the user-level server with the same name. `/mcp` offers "Enable in this project" and "Disable in this project", and `pig mcp list` shows the override. See `docs/mcp.md`.
- MCP OAuth `clientRegistration: "cimd"` signs in with Pi's Client ID Metadata Document on pi.dev instead of dynamic client registration, with a server-specific callback path when the authorization server does not send the `iss` parameter.
- `pi.registerToolRenderer()` in every SDK (Node `pi.registerToolRenderer`, Go `ToolRenderer`, Python and Rust `tool_renderer`) and for in-process Go extensions: resolvers choose how calls to any tool are drawn, in extension load order, in the transcript and in HTML exports. A resumed session draws calls to MCP tools whose server has not connected yet with the MCP renderers. An extension process answers once per tool, off the interactive loop, and `next()` cannot be wrapped (D89).
- Ctrl+X (`app.message.copy`) copies the sign-in URL on the `/login` dialog and the MCP sign-in screen.
- On Kitty-protocol terminals, non-PNG images are converted to PNG; a tool image that cannot be converted shows its text description instead of nothing.
- `check-model-data -hydrate <models.all.json>` hydrates a package's provider data from a published model catalog.

### Fixed

- "Selected model is at capacity" provider errors are retried.
- Cloudflare's System One classifiers that answer directly no longer fail to parse.
- Sign in with ChatGPT fails with a clear message when port 1455 is taken by another login or the Codex CLI.
- `--models` ignores empty entries, such as a trailing comma.
- In WezTerm fullscreen mode, a change to a row under an image no longer erases the image.
- Codemode stops a script whose output passes 16 MiB or 100,000 `text()`, `image()` and `console` calls, so a script printing in a loop cannot exhaust memory.

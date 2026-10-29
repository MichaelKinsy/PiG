### Changed

- The site and the agent docs bundle now share one source for their common pages. `automation/gen/gen-docs-bundle.py` copies 15 site pages (`json`, `llama-cpp`, `rpc`, `rpc-commands`, `rpc-extension-ui`, `session-format`, `usage`, `cli-integration`, `message-types`, `virtual-models`, `publishing`, `codemode`, `mcp`, `security`, `compaction`) and `how-pig-works` into the bundle, with bundle-local links, and `make generate` and `make docs-drift` keep them current. `internal/pigdocs/bundle-manifest.json` lists every bundle page and who owns it. A test fails when a generated page is edited by hand.
- New site and bundle pages: a glossary generated from `docs/project/CONTEXT.md`, a project trust page with a comparison to Pi, and a Piglet lifecycle page (author to update, with the Overlay Piglet and the tool scope). The site quickstart has a "Choose how to customize PiG" table that the bundle's `extend-pig.md` shares. Links to Pi's repository files now point at `blob/main`, as Pi's own docs do; a test rejects a tag link.

### Fixed

- The agent docs bundle no longer contradicts the site. `/fork` continues from before an earlier user message, the `PIG_HOME` default names `XDG_CONFIG_HOME`, `defaultThinkingLevel` defaults to `medium`, the site lists all nine Resource kinds, the bundle's Package page documents `pig update --models`, and the Piglet Image is marked as planned. <!-- fix: 0367abc801 -->

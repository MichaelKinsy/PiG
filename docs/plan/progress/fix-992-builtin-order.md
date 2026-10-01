# fix-992-builtin-order progress

Scope: P3 (builtin extension command order, scenarios 55, 28, 26) and P4 (slash popup count 1/25 vs Pi 1/24, `01-slash-popup`) from the Pi 0.99.2 parity aggregate. Oracle: the published Pi 0.99.2 package (`extensions/sdk-ts/node_modules/.bin/pi`) and its source mirror.

## Findings (root cause)

Pi: `extensions/index.ts:7-14` lists `builtInExtensions` as `llama.cpp, codemode, tool-search, mcp`. `package-manager.ts:970-986` appends each enabled `builtin:<name>` path in that order and `resource-loader.ts:703-735` loads them in that order, so `get_commands` (rpc-mode.ts:683) lists `/llama` (path `builtin:llama.cpp`) before `/mcp`. `BUILTIN_SLASH_COMMANDS` (`core/slash-commands.ts:19-44`) has 24 entries and does not contain `llama`; the popup (`interactive-mode.ts:691-775`) adds `/llama` only as an extension command, so `pi --no-extensions` shows `(1/24)`.

PiG: llama.cpp was a side channel instead of a member of the built-in extension list. (1) `SlashCommandCatalog.Inline` appended `/llama` after every runner command, so `mcp` came first (P3). (2) `defaultBuiltins()` listed `llama` as a builtin slash command, so it was in the popup and resolvable even with `--no-extensions` or `-builtin:llama.cpp` (P4, 25 entries).

Probe (Pi 0.99.2, scenario 55 agent fixture, `get_commands`): `... context-mode-probe, llama (builtin:llama.cpp), mcp (builtin:mcp), prompts ...`. PiG before the fix: `mcp` then `llama`.

## Red

No upstream test file in the 0.99.1 to 0.99.2 diff covers this scope (`diff -rq v0.99.1/packages/coding-agent/test v0.99.2/...` lists only codemode, mcp, tool-search, virtual-models, default-tools files; `resource-loader.test.ts`, `llama-extension.test.ts` are unchanged and already ported). The red tests are therefore PiG tests of the Pi rules above, with the upstream citations in each comment.

Red run (`e99aea644`, before any production change): 7 tests failed for the right reason.
- `cmd/pig`: `TestBuiltinExtensionsLoadInIndexOrderWithLlamaFirst`, `TestExplicitBuiltinExtensionsPrecedeTheIndexOrderedOnes`, `TestBuiltinLlamaIsNotReplaceableByAnotherLlamaCommand` (no `llama.cpp` entry in `builtInExtensions`), `TestRPCCatalogListsAndRunsBuiltInLlamaCommand` (`commands = [other llama mcp llama]`), `TestRPCGetCommandsListsBuiltinExtensionCommandsInIndexOrder` (`[mcp llama]`, want `[llama mcp]`).
- `internal/codingagent`: `TestSlashPopupWithoutExtensionsListsPisTwentyFourBuiltins` (25 entries), `TestSlashPopupListsLlamaAmongExtensionCommandsInLoadOrder` (27, `llama` twice), `TestLlamaSlashCommandResolvesOnlyWhileTheExtensionIsLoaded`, `TestBuiltinSlashCommandsFollowUpstreamOrder` (updated: `llama` is not in `BUILTIN_SLASH_COMMANDS`).
- `TestLlamaExtensionCommandRunsTheManager` passes in red on purpose: it guards that `/llama` still runs the manager once it is no longer a builtin.

## Green

`llama.cpp` is now the first entry of `nativeBuiltInExtensions` (`cmd/pig/builtin_extensions.go`, `extensions/index.ts:8`), a real member of the built-in extension list. Its factory (`cmd/pig/llama.go` `llamaExtension`) registers the `/llama` command with no handler: the llama host supplies the provider and each mode runs `/llama` with its own command context. Consequences:
- The loader orders `builtin:llama.cpp` with the other built-ins by the one rule Pi has (`package-manager.ts:970-986`, `resource-loader.ts:703-735`); the hosted-name special case (`hostedBuiltinExtensionNames`, the "skip hosted names" branch of `loadBuiltinExtensions`) and `SlashCommandCatalog.Inline` / `LlamaSlashCommand` are deleted (no remaining caller).
- `defaultBuiltins()` no longer lists `llama` (`slash-commands.ts:19-44`): 24 builtins, `/llama` only through the runner. `RunLlama` and `llamaHandler` are deleted. The interactive dynamic-command handler (`syncExtensionSlashCommands`) and the headless `executeCommand` recognise the llama command by source (`codingagent.IsLlamaCommand`) and run the llama host.
- `/llama` is not replaceable (no `replaceable` at `index.ts:8`): another extension that registers `/llama` yields `llama:1` / `llama:2` through the runner's normal resolution, and no bogus built-in conflict diagnostic.

Edits to existing PiG tests whose subject changed: `TestRPCCatalogListsAndRunsBuiltInLlamaCommand` (premise "inline extensions load last" was wrong for 0.99.2), `TestLlamaSlashHandlerAndLoginRouting` reduced to `TestLlamaLoginRouting` (the handler is gone), `TestConfigListsHostedBuiltinLlamaExtension` (passes `[]string{llamaBuiltinName}`), `test/docs-drift` (`/llama` recorded as an extension command). My red test `TestBuiltinExtensionsLoadInIndexOrderWithLlamaFirst` built its runner with `result.Host.Runtime()`, a nil Host without file extensions (test bug, nil deref); it now uses `inproc.NewRunner(result.Extensions, cwd)`.

Docs: `docs/site/docs/slash-commands.md` (`/llama` is registered by the built-in extension); `changelog.d/fix-992-builtin-order.md`.

## Evidence

Parity, serial, Pi 0.99.2 binary (`PIG_PARITY_PI_BIN=.../agg-992/extensions/sdk-ts/node_modules/.bin/pi`), all pass: `autocomplete` 01-slash-popup, 02-slash-filter-model, 03-slash-accept-hotkeys, 04-slash-dismiss, 12-editor-completion-lifecycle; `extensions-runtime` 55-package-manifest-startup, 28-prompt-precedence, 26-extension-load-order, 21-extension-tool-and-command-info, 55-node-package-entry-exports, 04-slash-command, 23-extension-directory-entries, 05-session-lifecycle; `rpc` 27-rpc-extension-discovered-resources. 55-package-manifest-startup and 01-slash-popup are the ones the aggregate listed as failing; 28-prompt-precedence and 26-extension-load-order passed too (they failed in the aggregate for the same order difference).

Mutations (each fails the named tests, then restored): llama.cpp moved to the end of `nativeBuiltInExtensions` fails `TestRPCGetCommandsListsBuiltinExtensionCommandsInIndexOrder` (`[mcp llama]`) and `TestCLIBuiltInExtensionsListMCPAfterToolSearch`; the interactive `/llama` intercept disabled fails `TestLlamaExtensionCommandRunsTheManager`.

Load: `GOMAXPROCS=4 taskset -c 0-3 go test -race -count=24` with four CPU burners on the new and touched tests of `cmd/pig` and `internal/codingagent` passes. `go test ./cmd/pig -timeout 40m` (859 s), `./internal/codingagent/...`, `./coding`, `./coding/extension/builtin/...`, `./coding/extension/host/inproc`, `./test/extension-conformance`, `./test/docs-drift` pass. Environment-only failures, unrelated: `internal/experimental` `TestPinnedUpstreamRadiusSource` needs `test/parity/interface-extractor/node_modules`; `coding/extension/host/subprocess` vendored-Pi tests need `npm ci` (this worktree symlinks `extensions/sdk-ts/node_modules` from agg-992 locally, untracked).

Gates: `go build ./...`, `go vet ./...`, `GOOS=windows go vet ./cmd/pig ./internal/codingagent`, gofmt, `go fix -diff`, golangci-lint (`--build-tags=integration,live,parity`, `./cmd/pig ./internal/codingagent/`: 0 issues), `check-public-claims.py` pass.

## Deferred / stubs

Nothing deferred; no stubs for other families. Not run: full `make parity` (the lane runs its scenarios only).

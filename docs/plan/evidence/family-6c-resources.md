# Lane port-99-f6c evidence (family 6, sub-lane 6C: resource loader, package manager, source info, `builtin:` naming)

Red run: `family-6c-resources.red.txt`. Tests use an isolated HOME, PIG_HOME and PIG_CODING_AGENT_DIR (lessons rule 17).

## Ported upstream cases (0.99.1 line numbers, `.upstream/v0.99.1/packages/coding-agent/test/`)

| upstream file | new or changed cases | Go test |
|---|---|---|
| `resource-loader.test.ts` | :43, :59, :88 (project manifest, host dependency warning, unparsable manifest) | `cmd/pig/extension_set_upstream_test.go` `TestUpstreamResourceLoader{ProjectManifestDoesNotOwnProjectExtension,WarnsAboutHostDependenciesInPackageManifest,FailsWhenPackageManifestCannotBeParsed}` |
| `resource-loader.test.ts` | :1040, :1085, :1107, :1126, :1149 (replaceable, disabled built-in, order, noExtensions, trust) | same file, `…LeavesOutReplaceableExtensions`, `…SkipsBuiltinExtensionsDisabledInSettings`, `…LoadsBuiltinExtensionsAfterFileExtensions`, `…NoExtensionsDisablesBuiltinsUnlessLoadedExplicitly`, `…AppliesProjectBuiltinOverridesAfterTrust` |
| `package-manager.test.ts` | :127 (built-in resolve) | `internal/packagemanager/builtin_extensions_test.go` |
| `package-manager.test.ts` | :798-1140 (git dependency install args, package manager name) | `cmd/pig/package_git_dependencies_upstream_test.go` (changed rows), `cmd/pig/package_commands_test.go` `TestGetGitDependencyInstallArgs`, `cmd/pig/npm_command_name_test.go` |
| `package-manager.test.ts` | :2647 (pinned temporary git source changes ref) | `cmd/pig/package_temporary_upstream_test.go` `TestPinnedTemporaryGitSourceChangingRefLoadsNewCheckout` plus the two rows whose checkout path now hashes the ref |
| `package-command-paths.test.ts` | :529, :555 (config built-in toggles) | `cmd/pig/config_builtin_upstream_test.go` |
| `extensions-discovery.test.ts` | :72 (ancestor manifests) | `coding/extension/host/subprocess/upstream_discovery_test.go` row, see per-case differences |
| `agent-session-dynamic-tools.test.ts` | :156 (`builtin:read`) | `coding/session_tool_registry_port_test.go`, `internal/codingagent/builtin_tools_scoping_test.go` |
| `system-prompt.test.ts` | :121 (docs list gains MCP) | `internal/codingagent/prompts/system_prompt_upstream_test.go`, `…second_half_upstream_test.go` |

Go tests without an upstream test file (source lines cited in each comment): `internal/codingagent/resource_source_info_test.go` (source-info.ts:14-29, paths.ts:45-64), `TestExtensionPackageWarningManifestShapes`, `TestOmitReplacedExtensionsWarnsAboutReplacedBuiltin`, `TestEnabledBuiltinExtensionPaths`, `TestBuiltinLlamaFollowsBuiltinExtensionSettings`, `TestExtensionWarningDiagnosticsFormatPackageWarnings`, `TestResourcePrecedenceRankPlacesBuiltinExtensionsLast`, `TestBuiltinResourceGroupLabelAndDisplayName`.

## Pending, not skipped

`resource-loader-theme.test.ts` (2 cases: `uses the $setting setting over a $environment environment`, `returns to automatic detection after an explicit setting is removed`; 3 rows) needs `getTerminalColorMode` and the capability overrides of upstream 0.99.1 `packages/tui` (family 5, branch port-99-f5, not in this base) and a theme JSON loaded per color mode (`resource-loader.ts:884-890`, `theme.ts loadThemeFromPath(path, colorMode)`, family 9). The Go loader (`internal/codingagent/extension_diagnostics.go loadThemeResources`) takes the color mode from the global capability cache, so the port is written once family 5 lands. It stays a hot-path pending row; the integrator must not close `resource-loader-theme.test.ts`.

## Per-case differences

1. `extensions-discovery.test.ts:72` (#9863). The upstream test expects the physical `node_modules` copy of `@earendil-works/pi-coding-agent` to answer the import of a compiled `.js` extension. That happens only in upstream's unbundled Node branch (`loader.ts:553-557`, jiti loads `.js` natively). PiG loads every extension through the embedded-module branch (`loader.ts:551-552`: `virtualModules`, `tryNative: false`; `runtime-node/jiti-loader.mjs importExtension`), where the host's own module answers. The Go row asserts what both branches share: the ancestor `package.json` neither fails the load nor changes it. Passed in red. Also `LoadExtensionsResult.warnings` is filled only by the resource loader upstream (`loader.ts:665,693`), so the no-warning half is `TestUpstreamResourceLoaderProjectManifestDoesNotOwnProjectExtension`.
2. `resource-loader.test.ts` cases use `.pi`, PiG uses `.pig` for the project directory. Inline extension factories are Go factories returning an `extension.Extension` (`inlineExtension.Factory`); the upstream `pi.registerCommand` call is the returned `Commands` map. `resolveProjectTrust` is `extensionSetLoader.Reload`'s callback.
3. `package-manager.test.ts:2647` spies `installParsedSource`; the Go test records the `git` binary instead.
4. `package-command-paths.test.ts:529,555` drive `ConfigSelectorComponent`; Go drives `tui.ConfigSelectorComponent.HandleInput(" ")` through `newScopedConfigSelector`.

## Passed in red (Go already matched)

`TestUpstreamExtensionsDiscovery` (all rows including ancestor manifests), the `TestUpstreamResourceLoader…` rows are red; the `clone-failure`, `dependency-failure` and `origin-head` rows of `TestPackageGitDependenciesOriginal`.

## Found on the way

`cmd/pig/config_command.go applyTopLevelToggle` writes nothing when a top-level resource is disabled (it deletes the entry and appends the pattern only when enabling). Upstream writes `-<pattern>` or `+<pattern>` (`config-selector.ts:545-560`). The built-in toggle test exposes it; fixed at the source in the green commit.

## Files outside the nominal lane list (scope expansion)

`internal/packagemanager/*` (the package manager), `cmd/pig/config_command.go`, `tui/config_selector.go` (built-in group), `internal/codingagent/extension_host_info.go` and `coding/session_tool_registry.go` (tool source path `builtin:<name>`), `coding/extension/extension.go` (one stub: `Hidden`, `Replaceable`; 6D owns the semantics), `cmd/pig/cli_runtime_build.go` and `cmd/pig/llama.go` (call sites), `internal/codingagent/prompts/coding.go` (docs list).

## Integrator notes

- `test/parity/runner/json_identity_test.go` (parity tag) holds the 0.87.1 oracle text of the docs section; it changes with the pin move.
- Ledgers not committed by this lane: PORT_MAP rows for `resource-loader.ts`, `package-manager.ts`, `source-info.ts`, `system-prompt.ts`, `package-manager-cli.ts`, `extensions/loader.ts`; the test mapping rows of the files above (re-hash after the pending theme cases port).

## Green (commit 1d0ac3423 and the follow-up)

Red to green: the 84 failing lines of `family-6c-resources.red.txt` pass. The ported tests are unchanged in the green commit. The one test file the green commit edits is `cmd/pig/llama_test.go`: its 0.87.1 expectation `<inline:llama.cpp>` became `builtin:llama.cpp` with source `builtin` (`agent-session.ts:3437,3472`). It is an existing test that belongs in the red commit; it was missed there.

Bug found by the full `cmd/pig` run and fixed at the source: gating the llama.cpp provider on `builtin:llama.cpp` leaves `build.Llama` nil under `--no-extensions`, and `refreshCatalogsInBackground` dereferenced it on RPC start (`cmd/pig/llama.go`). RPC tests such as `TestRPCModelMutationsDoNotRewriteDefaults` (started with `--no-extensions`) crashed the process, and pass with the nil guard. Those tests are the regression guard; a direct unit test cannot observe the goroutine.

Design notes:
- `cmd/pig/extension_set.go` mirrors `resource-loader.ts` `reload()`: a pre-trust pass with the project untrusted (user and CLI file extensions, inline extensions), the trust callback, then the final pass that loads the file extensions of the trusted project and the built-in extensions after them. Production startup (`cli_runtime_build.go`) uses the same functions for package warnings, built-in loading and replacement warnings; the registry `builtInExtensions` is empty until the extension-registering lanes fill it.
- The llama.cpp provider starts after trust resolution and only when `builtin:llama.cpp` is enabled.
- `-e builtin:<name>` paths load before the file extensions. Upstream orders them by flag position among the `-e` paths (`mergePaths(cliEnabled, enabled)`); PiG keeps the file extensions in the order `collectExtensionConfigs` returns and puts explicit built-ins first. They differ only when `-e builtin:x` follows `-e <file>`.
- `ReloadBuiltinExtensions` evaluates replaceable built-ins against the extension set of startup, so `/reload` re-evaluates replacement only once the registry has real entries.

Mutation checks (each applied to the production file, the ported tests run, then reverted): all 15 killed. Scope override, `+` override entries, last-`--` and ambiguity of `PackageManagerName`, the ref in the temporary checkout hash, `--no-extensions` gating, replaceable-owner skip, package origin, explicit built-in ordering, replaced-built-in message, project builtin toggle, `IsLocalPath` `builtin:`, precedence rank 5, tool source `builtin:<name>`, and the "Built-in (project override)" label. Two `PackageManagerName` mutations first survived because they ran against the wrong package, then died against `cmd/pig` where the tests are.

Load test (`GOMAXPROCS=4`, `taskset -c 0-3` with four busy loops on those cores, `-race -count=24`): the pure extension-set, config, package-manager-name, git-args and pinned-temporary tests, `internal/packagemanager`, and the `tui` config selector tests pass. The subprocess-backed `TestUpstreamResourceLoader*` cases pass with `-race -count=6` (each starts a Node host; under the burners one takes up to 18 s, so 24 repeats exceeded 40 min on a shared machine).

Gates: `go build ./...`, `GOOS=windows go build ./...`, `go vet ./...` (only the base failure below), `go fix -diff ./...` empty, `golangci-lint` 0 issues on the touched packages, `check-public-hygiene` and `check-public-claims` pass.

Full `cmd/pig` run: 25 failures, all also failing on the base commit or caused by the environment: the Pi oracle needs `extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent` (absent in this checkout); `TestRPCBedrockConverseStreamObservation`, `TestRPCMistralMatchesPi`, `TestRPCPiMessagesMatchesPi`, `TestRPCStdoutBackpressureLetsProviderFinishBufferedBody` and `TestJSONModeStdoutBackpressureMatchesPi` fail identically on 15904a765. `./internal/... ./tui/... ./coding/...`: only Xvfb missing, the `typescript` module missing for the interface extractor, and the RPC33 oracles pinned to 0.99.1 while `UpstreamVersion` is still 0.87.1.

Not run: `make parity-family` refuses to run here ("refusing to install through shared path extensions/sdk-ts/node_modules"), so no paired Pi scenario ran for this lane. `make ci-contracts` stops at `interface-go-drift` (`test/parity/interfaces/pig-go.json` is stale because exported API changed: `NpmCommandName` removed, `PackageManagerName`, `GetGitDependencyInstallArgs`, `ResolveBuiltinExtensions`, `ResolveBuiltinExtensionSources`, `SyntheticPathSource`, `IsSyntheticPath`, `BuiltinPathPrefix`, `extension.Extension.Hidden/Replaceable`). The integrator regenerates it. `test-inventory`, `test-porting-release` and `known-gaps-drift` fail on a missing file of another family (`ai/images_models_upstream_test.go`).

## Hand-offs to other lanes

- 6D: `extension.Extension.Hidden` and `Replaceable` are fields only. The startup Extensions list must skip hidden extensions (`interactive-mode.ts:1762`): that needs `inproc.ExtensionSource.Hidden` and `internal/codingagent/loaded_resources.go loadedExtensionResources`.
- Extension registration (families 6B, 8): register built-ins as `inlineExtension{Name, Builtin: true, Replaceable, Factory}` in `cmd/pig/extension_set.go builtInExtensions`. A built-in that runs as a subprocess (codemode, tool-search) has no path here yet.
- A Piglet's ambient-source setting does not gate built-ins.

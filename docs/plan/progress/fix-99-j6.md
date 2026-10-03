# fix-99-j6: the last unowned 0.4.0 failures

Base: `63ac0e740` (staging `porter/pi-0.99.1`). Comparator: exact upstream 0.99.1 from `make parity-deps` (`extensions/sdk-ts/node_modules/.bin/pi`).

## Findings

1. **`coding` `TestUpstreamCodemodeOptionsAndStore` (bash structured result).** Also fails on `port-99-f6h-model-types` `4a26d79b5`, so it is not dropped. Cause: `toolDefinition` (`coding/session_tool_registry.go`) never copies a built-in tool's `OutputSchema` into its `ToolDefinition`. Pi's `tool-definition-wrapper.ts:17,52` carries `outputSchema` both ways and `bash.ts:262` declares one; codemode resolves a nested call to `structuredContent` only for a tool whose definition has the schema (`extensions/codemode/execute.ts:207`). Without it `bash` with a non-zero exit code rejects the script instead of resolving to `{output, exit_code, ...}`.
2. **Settings `(1/35)` vs `(1/34)`.** Pig lists one row upstream 0.99.1 does not: D80 "Mask secret input" (`internal/codingagent/slash_session_handlers.go`, owner-approved). upstream 0.99.1 lists 34 rows with image controls, 32 without (23 base + 2 image + 9 always; `settings-selector.ts:481-857`); upstream 0.99.1 added `fullscreen-wheel-scroll-lines` (`settings-selector.ts:733`) to the 0.87.1 list, and Pig already lists it. Scenarios `settings/02`, `settings/09` assert the stale 0.87.1 counts `(1/32)`/`(1/31)`; `clipboard-images/05` never asserted or mapped the D80 row. Pig is right; the scenarios are wrong.
3. **Autocomplete menu colour.** `test/parity/testdata/editor-completion-helper/pi.mjs` hard-codes Pi 0.87.1's teal accent `38;2;138;190;183`. upstream 0.99.1's dark theme accent is `violet` `okhsl(295, 50, 67)` (hue, saturation, lightness) = `38;2;167;152;215` (`theme/dark.json:16`; `theme.ts:1137` `selectedText: theme.fg("accent")`). Pig is right; the Pi-side probe used a stale colour. Fix: build the probe's editor theme with Pi's own `getEditorTheme()` after `initTheme("dark")`.
4. **`extensions-runtime/21-packed-log-reload-cleanup` socket path.** Nothing leaks into compared output; the packed Node cell cannot start because `<TMPDIR>/pig-<uid>/host-<n>/e-0.sock` exceeds the Unix socket limit (103 bytes on darwin). The scenario sets `TMPDIR={{TEMP}}` (`<TMPDIR>/parity-tmux-21-packed-log-reload-cleanup-pig-<10 digits>`), so the path depends on the random digits. Root cause is in the host: a deep `$TMPDIR` breaks every extension start (Pi's in-process extensions have no such limit). Fix: `socketRuntimeBase` falls back to `/tmp/pig-<uid>` when a socket path below the configured directory could exceed the limit.

## Red

Commit `test(...)`: `TestToolDefinitionCarriesOutputSchemaOfBuiltinTools`, `TestSocketRuntimeBase*`, `TestSockPathForUsesAShortDirectoryWhenTheSocketDirectoryIsDeep`, with the signature stub `socketRuntimeBase`. `TestUpstreamCodemodeOptionsAndStore` (already ported) is the red for item 1.

Red run:
- `go test ./coding -run 'TestUpstreamCodemodeOptionsAndStore|TestToolDefinitionCarriesOutputSchema'`: FAIL (`isError: Script error: Error: out ... Command exited with code 3`; `the bash definition has no outputSchema`).
- `go test ./coding/extension/host/subprocess -run 'TestSocketRuntimeBase|TestSockPathFor'`: FAIL (`want "/tmp/pig-501"`; `Unix socket path is 232 bytes; linux supports at most 107`).
- Items 2 and 3 are wrong parity oracles; their scenarios fail today (`make parity-family` runs recorded below) and pass after the oracle fix.

## Green (items 1 to 4)

- `4eb498255`: `toolDefinition` copies a built-in tool's `OutputSchema` (`coding/session_tool_registry.go`); `socketRuntimeBase` (`platform.go`, used by `ensureSockRuntimeDir` in `host.go`) falls back to `/tmp/pig-<uid>` when a socket path below the configured directory could exceed the platform limit. The ported test moved one constant (`worstRuntimeSocketSuffix`) into production; no assertion changed.
- `dc8c01598`: scenario/oracle fixes: `settings/02`, `settings/09` (counts `(1/33)`/`(1/32)` with the D80 map), `clipboard-images/05` (same assertions and map added), `autocomplete/12` (`pi.mjs` uses Pi's `getEditorTheme()` after `initTheme("dark")`).
- Mutation checks: dropping the `OutputSchema` copy fails `TestUpstreamCodemodeOptionsAndStore` and `TestToolDefinitionCarriesOutputSchemaOfBuiltinTools`; disabling the fallback fails `TestSocketRuntimeBaseFallsBackWhenASocketPathCouldExceedTheLimit`, `TestSockPathForUsesAShortDirectoryWhenTheSocketDirectoryIsDeep`, and scenario `21-packed-log-reload-cleanup` with `TMPDIR` 53 bytes long and `XDG_RUNTIME_DIR` unset (`Unix socket path is 148 bytes; linux supports at most 107`); the fixed binary passes it.
- Scenario runs on Linux with the exact upstream 0.99.1 comparator (`runs=1`): `02`, `09`, `12`, `21` pass. `clipboard-images/05` needs `ht` (Homebrew), which is not on this host: the scenario is skipped here, and its fix is the same D80 map, with the row counts derived from `settings-selector.ts`. A tmux emulation of the Kitty environment did not deliver keys, so 05 is unverified on this host.
- Not attributable to this lane: `cmd/pig` `TestRPCPlanModeEmptyToolsSurviveProcessResume` fails on `staging` `63ac0e740` too; `TestRPCFreshPromptDeclaresStructuredSystemOnce` is fix-99-iserror's. `coding/extension/host/subprocess` `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` needs `Xvfb`, which this host lacks.

## Item 5 (per-extension `tools: []` in print, JSON mode)

Reproduced with the Pigpen test (`rev-pigpen-release` `scripts/tool-scope.test.mjs`, marked todo there): print and JSON mode offer `pig_doctor` (`[read bash edit write pig_doctor]`); RPC and interactive do not. Cause: the Piglet built-in extension (`coding/piglet/main.go` `applyScoping`) derives a tool's owning entry from `ToolInfo.SourceInfo`. Session `GetAllTools` (used for print, JSON and RPC context actions) reports Pi's provenance object (`map{path,source,scope,origin}` for extension tools, `PiSourceInfo` for built-ins), which names no Piglet entry, so the tool scoped as a built-in and `ScopeTools` kept it (`04eab6dd2` fixed only the missing binding, so a Piglet-wide `tools: []` worked). Interactive mode reads runner tools whose per-tool source is the D23 string, so it worked; RPC scoped a second time from `st.ToolSource`.

Fix: `piglet.BuildExtensionWithPigletTools(initial, owner)`, with `pigletToolOwner` (`cmd/pig/piglet_tool_owner.go`) resolving the owning entry from the registering extension's `RegisteredTool` (D23 string, else extension name), wired in `withPiglet` (`cli_runtime_build.go`), which every mode shares. RPC's own `ToolSource` map now uses the same owner (one mechanism). The session's `getAllTools` keeps Pi's provenance object.

- Red `0e01f021a`: `TestBuiltExtensionScopesToolsByTheOwningPigletEntry` (`coding/piglet`, two cases fail) and `TestPigletExtensionToolsScopeInEveryMode` (`cmd/pig`, real binary, fake OpenAI endpoint, Node extension, 4 modes x 4 lists): print and JSON fail for `tools: []` and `tools: [other_tool]`; RPC, interactive and the controls (`tools: [scoped_tool]`, no list) pass.
- Green `2bf1c186c`: all pass. Mutation check: the owner returning `""` fails the same cases. With the fix, the unmodified Pigpen test with the todo removed passes all three modes against the fixed binary (`/tmp` build; the Pigpen tree is untouched, so the todo stays there until the pin moves).

## Item 6 (`typedModels` recomputed on every registry state)

Merged `staging/rev-port-99-f6h-model-types` `5f83c3125` (`e669cf59a`). `BenchmarkExtensionCatalogEncoding/cached` (this host, `-benchtime 200x`, `GOMAXPROCS` default):

| tree | B/op | allocs/op |
|---|---:|---:|
| base `113a10ba9` | 11.3 M | 21.7 k |
| merged f6h, before | 12.65 M | 32.1 k |
| after (`perf(codingagent)` commit) | 11.4 M | 21.7 k |

`extensionCatalogEncoding` now retains the typed-model encoding and its providers, and drops them when the chat catalog encoding changes or on `invalidateTyped`, which `WireModelOperations` calls from the registry change listener that also republishes the catalog (the same events). A generation counter stops a publication that raced a change from storing its stale result.

- Red `test(codingagent)` commit: `TestExtensionCatalogEncodingReusesTypedModelsUntilTheRegistryChanges` (fails: nothing retained) with the stub `invalidateTyped`; `TestExtensionRegistryStateTypedModelsFollowRegistrations` is a stale-cache guard through the real `WireModelOperations` (passes before, must keep passing).
- Mutation checks: not invalidating from the listener fails the guard (`the registered image model is missing from the state read after the registration`); disabling reuse fails the reuse test.
- Found by `-race -count=24`: `ai.RegisterBuiltInImagesAPIProvider(s)` wrote an unlocked global map on every typed-provider composition (`builtinTypedBase`), so two concurrent registry states raced (pre-existing in the merged f6h code). Fixed with an `RWMutex` in `ai/images.go`; `TestImagesAPIProviderRegistryIsSafeForConcurrentUse` fails under `-race` before the fix.

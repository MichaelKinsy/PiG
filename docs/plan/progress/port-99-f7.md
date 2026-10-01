# Family 7: RPC dispositions, print/json, CLI (`pi mcp`, help)

Lane `port-99-f7`, plan Phase 2 step 7. Upstream: `.upstream/v0.99.1/packages/coding-agent`.

## Scope found

The upstream diff 0.87.1 → 0.99.1 for this family is small and exact:

| upstream | change | Go |
|---|---|---|
| `src/modes/rpc/rpc-mode.ts:403-407,419-425`, `rpc-types.ts:117-126` | `prompt`, `steer`, `follow_up` responses carry `data: {disposition}` | `cmd/pig/rpc_admission.go`, `rpc_mode.go`, `rpc_types.go` |
| `src/modes/rpc/rpc-client.ts:198-221,535-537` | `prompt()` takes `streamingBehavior` and returns the disposition; `steer()`/`followUp()` return it; listeners iterate a snapshot | `coding/rpcclient` |
| `src/main.ts:608-612`, `src/extensions/mcp/cli.ts` | `pi mcp add|remove|list|login|logout` | `coding/mcpext/cli.go`, `cmd/pig/mcp_command.go` |
| `src/cli/args.ts:285-286,313-314` | help text: `mcp <command>`, `builtin:<name>`, `--no-extensions` wording | `cmd/pig/help_upstream.txt` |
| `src/modes/print-mode.ts`, `json-event.ts`, `cli.ts` | unchanged | none |
| `src/main.ts:796-799` (extension package warnings), `package-manager-cli.ts` (`builtinExtensions`), `--extension builtin:` and `--no-extensions` semantics | family 6C (already merged: `cmd/pig/extension_set.go`, `config_command.go`) | none here |

Test files: `rpc-prompt-response-semantics.test.ts` (4 → 7 cases; 4 new case ids, 3 changed bodies), `mcp-command.test.ts` (8 new cases). `suite/regressions/7150-rpc-prompt-during-compaction.test.ts` (`preflightResult` is undefined when compaction rejects the prompt) belongs to 6E and is ported there (`coding/session_prompt_disposition_upstream_test.go`). The `--import` file-URL edits in `stdout-cleanliness`, `experimental-cli-entry`, `startup-session-name`, `session-file-invalid` and `session-id-readonly` change only how the Node test spawns its child (`pathToFileURL`); there is no Go behavior to test (designed out per case, no Node loader in Go).

Print and JSON modes: upstream changed nothing in `print-mode.ts` or `json-event.ts`. What changed for them is the `thinkingLevel` field on assistant messages (agent loop, family 4) and the disposition-free `preflightResult` (6E). Verified below.

## Red

Commit `ecadebada` (`test(cli): ... (red)`).

Stubs: `rpcclient.Prompt/Steer/FollowUp` (new signatures, return a not-implemented error) and `mcpext.RunMcpCommand` (returns -1).

| Go test | upstream case | red result |
|---|---|---|
| `TestRPCPromptResponseSemanticsUpstream/emits one failure response when prompt preflight rejects` | `:197` | passes (failure responses carry no data, unchanged) |
| `.../emits one started response when prompt preflight succeeds` | `:237` | fails: response has no `data.disposition` |
| `.../reports extension commands and intercepted input as handled without starting a run` | `:259` | fails: no `data.disposition` |
| `.../emits one success response when prompt is queued during streaming` | `:291` | fails: no `data.disposition` |
| `.../reports {steer,follow_up} as handled even when an extension queues another message` (2) | `:329` | fail: no `data.disposition` |
| `.../reports {steer,follow_up} as queued after input transformation` (2) | `:377` | fail: no `data.disposition` |
| `.../returns and clears queued steering and follow-up messages` | `:411` | fails: `clear-start` has no `data.disposition` |
| `TestRPCPreflightAdmission/second prompt and queues do not wait for preflight` | Go-only, asserts the `first` prompt is `started` | fails |
| `TestMcpCommand*` (7 Go tests for the 8 `mcp-command.test.ts` cases; the "rejects invalid add invocations" one asserts exit 1 and no file, so it needs the stub to return something other than 1) | `mcp-command.test.ts:47-203` | 7 fail; the invalid-add one also fails because the stub returns -1 |
| `TestPigMcpRunsBeforeSessionStartup`, `TestPigMcpHelpIsPisHelpWithPiGsIdentity`, `TestPigMcpReadsProjectConfigOnlyForTrustedProjects` | `main.ts:608-612`, `cli.ts:196-197` | fail: `mcp` is treated as a message |
| `TestHelpMentionsMcpCommandAndBuiltinExtensions` | `args.ts:285-286,313-314` | fails |
| `TestRpcClientPromptSteerAndFollowUpReturnTheDisposition`, `TestRpcClientDispositionOfAResponseWithoutDataIsAnError`, `TestRpcClientListenerUnsubscribeDuringDispatchDoesNotSkipLaterListeners`, `TestRpcClientListenerSubscribedDuringDispatchWaitsForTheNextEvent` | `rpc-client.ts:198-221,535` | fail |

Per-case forced differences in the `mcp-command` port: the test binary is the stdio fixture server (`fixtures_test.go` `stdio-server`, a port of `packages/mcp/test/fixtures/stdio-server.mjs`) where upstream spawns `process.execPath FIXTURE`, and PiG's identity (`pig`, `.pig`) replaces `pi`/`.pi` in expected text (`If it requires sign-in: pig mcp login sentry`, `.pig/mcp.json`), the same substitution `automation/gen/gen-help.sh` applies to `--help`.

The six shutdown/input-end families the lead's joint run 4 reported (`TestRPCInputEnd*`, `TestRPCShutdown*`, `TestRPCExtensionShutdownRequestExits`, all in `cmd/pig/rpc_shutdown*_test.go`) were red for the same reason: real upstream answers a prompt with `data: {disposition: "handled"}` and PiG sent no data. They encode the pre-change response shape in their expectations, so the green commit updates them (`handledPromptResponse(id)` helper); each cites `rpc-mode.ts:403-425`. They were not in the red commit because the oracle (`/pi` variants) only runs against the real package, which this lane installed later.

## Green

| commit | what |
|---|---|
| `b73879b6c` | RPC: `prompt`/`steer`/`follow_up` answer with `data.disposition` (`cmd/pig/rpc_admission.go`, `rpc_types.go`); `coding/rpcclient` `Prompt/Steer/FollowUp` return it and `emitEvent` iterates a snapshot (`rpc-client.ts:198-221,535`); shutdown/queue tests and scenarios `rpc/06,13,17,20,25` expect `data.disposition` |
| `6f1c795a3` | `pig mcp add|remove|list|login|logout` (`coding/mcpext/cli.go` port of `cli.ts`; `cmd/pig/mcp_command.go`, stripped variant under `pig_strip_mcp`), `main.go` `case "mcp"`, the global `httpProxy` setting applied first (`main.ts:591-593`), `help_upstream.txt` regenerated with `automation/gen/gen-help.sh` from the real package (byte-identical to the hand edit), `mcp/stdio.go` reports a missing program as Node does (`spawn X ENOENT`; `TestStdioSpawnFailure*`, written red first) |
| merge `4118f393d` | staging `porter/pi-0.99.1` at `e053c1a2a` (clean) |
| `ab90b3e92` | parity runner starts pig with `--no-extensions` like the pi oracle (lead answer to Q1); `TestNoExtensionsSkipsBuiltInExtensionsButExplicitBuiltinStillLoads` pins the behavior (characterization: green on first run, PiG already matched; probed identical against the real package for the default, `--no-extensions`, `--no-extensions -e builtin:llama.cpp`, and `-e builtin:nope`) |
| red `test(extension)` + green `fix(extension)` | F1: getAllTools reports `exposure` (default `direct`), `namespace`, `annotations` (`types.ts:2063`, `agent-session.ts:1449-1460`); red = `TestExtensionToolInfosReportExposureNamespaceAndAnnotations` (6 failures), wire stub fields on `subprocess.ToolInfo` |
| docs | `docs/extension-api-parity.md` getAllTools row |

Differential probe: `/tmp` script running identical `pi mcp` and `pig mcp` setups (about 70 invocations: help, list, `--json`, login/logout errors, add/remove variants, trusted and untrusted project config) and diffing normalized output: all identical except `.pi`/`.pig` paths. Extra Go tests beyond the upstream ones: `coding/mcpext/cli_test.go` (help golden = real `pi mcp --help` with the identity substitution, chalk styling, 21 argument-error cases, empty/JSON lists with member order, project trust note, OAuth sign-in/sign-out cycle against the fake server, pasted-redirect path, `--timeout` rounding and non-positive values, concurrent server connection via a rendezvous fixture), `TestPigMcpAppliesTheHTTPProxySetting`.

### Mutation checks

RPC dispositions (6 mutants, all killed by `TestRPCPromptResponseSemanticsUpstream`, `TestRPCQueueRepliesYieldToInputBatch`, `TestRPCPreflightAdmission`): queue promise handled→queued, command-prompt handled→started, queued→started, started→queued, steer queued→handled, input-handled→started. `pig mcp` (11 mutants): sign-in hint always on, list ignores `enabled`, positional cap removed, `Math.round` → floor (survived; added the `--timeout 1.6` case), `-l` alias, url/command check removed, tool-override comparison inverted, replaced verb inverted, misplaced-option check removed, trust note removed, concurrent connect → sequential (survived; added the rendezvous test, which the sequential mutant fails after its 20 s bound). All killed after the two additions.

### Load

`taskset -c 0-3`, `GOMAXPROCS=4`, six CPU burners on those cores, `-race -count=24`: `coding/mcpext` `TestMcpCommand*` (262 s), `coding/rpcclient` `TestRpcClient*` (87 s), `mcp` `TestStdioTransport*` (45 s), `cmd/pig` `TestRPCPromptResponseSemanticsUpstream` (410 s), `TestRPCPreflightAdmission` + `TestRPCQueueRepliesYieldToInputBatch` (655 s): all pass. (A first combined run hit the default 10 minute `go test` timeout, not a failure; rerun with `-timeout 40m`.)

### Parity against the real package (0.99.1 installed in `/tmp`, symlinked as the local `extensions/sdk-ts/node_modules`)

Families json, print, cli-utils, rpc run directly through the runner with `-pig-parity.tags=hermetic`. After the runner and F1 changes json and print pass entirely. Remaining reds, none in this lane's scope and each present without this lane's changes:

| scenario | cause | owner |
|---|---|---|
| `rpc/41-rpc-tools-allowlist-overrides-no-tools`, `rpc/26-rpc-session-stats-context-estimate` | bash tool result lacks `structuredContent` (`exit_code`, `output`, `truncated`, `wall_time_seconds`) | tools family |
| `rpc/17-rpc-abort-retry` | assistant error message carries `thinkingLevel` in PiG only (`"thinkingLevel":"off"`) | family 4 |
| `rpc/33-rpc-real-provider-records-anthropic` | anthropic streaming `message_start` carries the partial tool/text block upstream | provider streaming (family 2) |
| `rpc/33-rpc-wire-mutations` | `estimatedTokensAfter` 783 vs 782 after compaction | compaction |
| `cli-utils/10-startup-resume-picker-cancel` | picker rectangle colors: PiG 24-bit `38;2;138;190;183`, oracle `38;5;5` (also under `COLORTERM` unset) | selectors/TUI |
| `cli-utils/07-login-github-copilot` | wrapper cannot locate the pi-ai cli in the local install (environment) | environment |
| `TestCLIFixtureProjectsRunOutsideCheckout` | `model-runtime-store-catalog/16-image-model-data.toml`: covers must list at least one upstream file | oracles/ledgers |

`coding/extension/host/subprocess` also fails vendoring tests (`TestVendoredPiDistMatchesThePinnedPackage`, `TestNodeSDKBundleRegeneratesExactly`, ...) against my unlocked `npm install` of the package; they do not touch this lane.

### Open observations for other lanes

- `pig --mode rpc get_commands` lists `llama` but not upstream's `mcp` built-in command by default (probed: upstream lists `llama`, `mcp`). MCP built-in surface, not implemented here.
- `docs/site` and pi-dist doc mirrors (`rpc.md`, `rpc-commands.md`, `cli.md`, `mcp.md`) carry upstream's disposition text; they belong to the docs sweep.

# gap-experimental closure ledger

Slice: `packages/coding-agent/src/experimental` (excluding `services/`) and `experimental/plugins`. Base `12429f15b` (Pi 1.0.4). The integration step applies these verdicts to `docs/parity/PORT_MAP.md`; this lane does not edit it.

Method: read each upstream file against its Go side; run the whole `internal/experimental/...` tree (with the pinned upstream oracles, which need `make parity-deps`); mutate the Go side and keep every mutation a test did not kill as a finding; qualify the composed path through the built `pig_experimental` executable.

Upstream 1.0.0 → 1.0.4 left every file of this slice and every covering `test/experimental-*.test.ts` byte-identical (`diff` of the mirrors), so the 1.0.0 port the Go side carries is the 1.0.4 behaviour. Pi's `experimental/` is not in the published package, so no real-Pi run of `cli.ts` exists to pair against: the upstream source needs generated `packages/ai/src/providers/data/.manifest.json`, which the mirror lacks. The `internal/experimental` oracle tests execute the pinned `coordinator-entry.ts`, `radius-*.ts` and `process.ts` directly where they can.

## Row verdicts

All 15 rows are DONE. None needed a new port; the open ⬜ rows were stale (the Go files and tests existed). The Go behaviour fixes are the `server.ts:469` aggregate message and, in review, the `package.ts:167` empty-basename label.

| row (upstream path) | verdict | evidence |
|---|---|---|
| `experimental/cli.ts` | DONE | `cmd/pig/main_experimental.go` role dispatch and stable fallback: `cmd/pig-experimental` `TestDevelopmentCLIEntryUpstream` (ports `experimental-cli-entry.test.ts`) and `TestDevelopmentEntryServerClientSessionLifecycle` |
| `experimental/client-runtime.ts` | DONE (Radius :57-68,125-133,156-169 is D64) | `TestOpenClientRuntimeValidationPrecedesDiscovery`, new `TestClientRuntimeRejectsAmbiguousServerSelection`, new `TestRouteFromExplicitPathRequiresAServerIDSocketName`, `TestExperimentalDurableServerCompositionA` and `...RemoteB`, the lifecycle test |
| `experimental/client-tui-chat.ts` | DONE | `TestClientChatViewReusesStreamingComponentAndRebasesDivergentPrefix`, `TestClientQueueWhitespacePreservesBoundarySpaces`, new `TestClientChatViewStatusText` and `TestClientChatViewMarksCompactionAndReset`, new `TestClientTuiStatusTexts` (the interim and final status of an unknown slash command, a prompt, a steering message and a follow-up; thirteen message mutations, all killed; review added the accepted-prompt row, because removing the `client-tui.ts:588` clear survived) |
| `experimental/client-tui.ts` | DONE (Radius status text is D64) | `TestExperimentalClientTuiUpstream` (ports `experimental-client-tui.test.ts`), the `TestClientTui*` component tests, new `TestClientTuiStatusTexts`, `TestRunClientTuiOwnsTheCustomThemeWatcherOnARealTerminal` |
| `experimental/client.ts` | DONE (the `transport === "radius"` test at :59 is D64) | `TestPromptClientSessionWaitsForTheAnswer`, `TestClientResultJSONShapes`, remote runtime tests, new `TestClientRuntimeRejectsAmbiguousServerSelection`, the lifecycle test |
| `experimental/commands.ts` | DONE (Radius relay status lines :14-29 are D64) | the lifecycle test drives `server` (prints `Server:`/`Socket:`, SIGTERM exit 0) and `client` (list, prompt, `--connect`); mutating the `Socket:` line fails it |
| `experimental/coordinator-entry.ts` | DONE (the row was stale ⬜) | `RunCoordinatorEntry`; `TestCoordinatorEntryRejectsNonInternalRole`, `TestEnsureCoordinatorSpawnsNativeEntry`; mutating its role check fails the first |
| `experimental/coordinator.ts` | DONE (the row was stale ⬜) | `coordinator.go`, `coordinator_connection.go`, `coordinator_transport.go`; every `TestCoordinator*` runs its upstream=true leg against the pinned `coordinator-entry.ts`; setting `CoordinatorProtocolVersion` to 4 fails `TestCoordinatorGenerationRouting` and `TestCoordinatorConnectionLifecycle` |
| `experimental/plugins/bundled.ts` | DONE | `TestServerSelectedPresentationFacetsUpstream`, `TestNodeFacetsRegisterReplaceRunAndDispose`; renaming `presentationFacetBundles` fails both. `node-facets/plugin.mjs` implements `experimental/plugin.ts` (the three service IDs match `services/*.ts`), so the PORT_MAP `plugin.ts` n/a row is implemented, not designed out |
| `experimental/plugins/package.ts` | DONE | `TestPortWave08ServerProfile`, `TestBundleFacetsOwnsOutputPathsVersionAndReplacement`, `TestBundleFacetPackageUsesCallerConventionsAndSharedServerCompiler`, new `TestPluginPackageOnDiskNames` (the Session profile digest and the build directory label and digest; independent SHA-256 values; the mutations `[:24]`, `[:12]` and `"-plugin"` were not caught before it; review added the `.`/`_` label row and the filesystem-root rows, which fail on the previous `filepath.Base` label) |
| `experimental/process.ts` | DONE (the row was stale ⬜; the `--import source-resolver.ts` branch is Node-only) | `TestUpstreamInternalProcess` (runs the pinned `process.ts`), `TestEncodeControlLineExactUpstreamJSON`, `TestSpawnInternalProcess`, `TestTerminateInternalProcess`; mutating the 128 MiB value fails `TestEncodeControlLineExactUpstreamJSON` (not `TestUpstreamInternalProcess`); `TestEncodeControlLine` pins the `process.ts:104` boundary one byte over the limit |
| `experimental/radius-auth.ts` | DONE (library; no application path selects it, D64) | `TestRadiusAuth*`, `TestPinnedUpstreamRadiusSource`, new `TestRadiusAuthRefreshThresholdIsFiveMinutes` (a six-minute threshold is killed; a four-minute one is equivalent because `ai/auth_resolve.go` already floors the window at five minutes) |
| `experimental/radius-relay.ts` | DONE (library; composition is D64) | `TestRadiusHost*`, `TestRadiusClient*`, `TestRadiusWriter*`, `TestRadiusReconnect*`, `TestRadiusRealFakeGateway`; the retry start (1 s) and the pinned upstream source run; review added `TestRadiusHostRetryBackoffDoublesToThirtySeconds`, `TestRadiusHostConnectionResetsRetryBackoff`, `TestRadiusHostMissingAuthRetriesEveryThirtySeconds` and `TestRadiusClientReconnectBackoffDoublesToThirtySeconds`, and split the shared retry constants into the five upstream ones (`radius-relay.ts:16-20`) |
| `experimental/server.ts` | DONE (relay construction :637-677 is D64) | `TestPortWave08ServerLifecycle`, `TestServerLifetime*`, `TestPortWave08ServerProfile`, the lifecycle test (cold activation, retirement, foreground server); fixed the aggregate message at :469 with new `TestBackendClosureReportsServerAndRepositoryFailures` |
| `experimental/session-worker-manager.ts` | DONE | `TestPortWave08WorkerManager` and the `TestSessionWorker*` family (nine upstream cases plus ordering, cancellation and shutdown races); the lifecycle test runs a real worker (spawn, resume of its durable history, retirement) |

## Findings

1. Bug fixed at the source (`internal/experimental/server_runtime.go`): the backend closure aggregate said "Server and catalog shutdown failed"; `server.ts:469` says "Server and repository shutdown failed".
2. Tests added for behaviour that mutation showed unpinned: the `radius-auth.ts:48` refresh threshold, the `package.ts` on-disk names (shared with a Pi server under D2's Pi namespace), three user-visible client diagnostics, the chat view status line, and the compaction/reset markers.
3. Composed qualification: `TestDevelopmentEntryServerClientSessionLifecycle` builds the `pig_experimental` executable and drives a cold prompt against a local OpenAI-compatible stub: activation of a detached server, coordinator and Session worker; the answer through the real coding Harness; list; resume with the durable `user, assistant, user` history; foreground server with `--connect`; SIGTERM exit 0. Unix only (Windows rejects the Unix transport, as Pi does).
4. `make divergence-guard` does not pin time constants: it accepts a literal whose digits appear anywhere in the cited upstream file. Review showed `HOST_RETRY_MAX_MS` 30 s → 20 s surviving both the tests and the guard ("20" occurs in `radius-relay.ts:613`), and added the backoff tests above.

## Observations for the integrator

- `experimental/plugin.ts` is marked n/a in PORT_MAP, but `internal/experimental/node-facets/plugin.mjs` implements its service tokens and `TestNodeFacetsRegisterReplaceRunAndDispose` and `TestServerSelectedPresentationFacetsUpstream` exercise it.
- The `cli/experimental/*` rows (🟡) live outside this slice; they share the Go files `command_parse.go` and `commands*.go`, which the lifecycle test exercises (flags `--provider`, `--model`, `--session-id`, `--connect`, a positional prompt).
- `test/parity/interfaces/test-mapping-v1.0.4.json` rationale for `experimental-remote-runtime.test.ts` and `experimental-cli-entry.test.ts` can cite the lifecycle test.

## Divergences

None proposed. Every Radius branch is already recorded as D64.

## Open

None.

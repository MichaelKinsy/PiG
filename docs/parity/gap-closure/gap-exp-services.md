# Gap closure: experimental services and experimental CLI (Pi 1.0.4)

Slice: `packages/coding-agent/src/experimental/services` and `packages/coding-agent/src/cli/experimental`. Base `12429f15b`. Mirror `.upstream/v1.0.4`.

Finding: none of the 13 upstream files changed between Pi 1.0.0 and 1.0.4, and all 18 `experimental-*.test.ts` files are already `ported` in `test-mapping-v1.0.4.json`. Every row's Go side was compared line by line against its upstream file. No missing behavior was found. The rows stayed 🟡 or ⬜ only because their notes said "runtime qualification pending", "compile-only qualification", or "pending". This lane replaces those claims with mutation-proven behavioral tests: each production decision was mutated (member names, forwarded arguments, fences, messages, ordering) and the mutants that survived the existing suite were closed with new tests that fail on the mutant.

Verdicts: 13 DONE, 0 PORTED, 0 DIVERGENCE-PROPOSED, 0 open. The CLI rows keep one already-numbered, owner-approved divergence (D64, Unix-only experimental transport). It needs no new number.

Integration step (not done here, per lane rules): set the 13 PORT_MAP rows to ✅ with the evidence below; add `// Ports packages/coding-agent/src/experimental/services/server.ts` to `internal/experimental/services/server.go` and `…/plugins.ts` to `plugins.go` in the same change as the PORT_MAP edit (`make port-map-drift` rejects the marker while the row is ⬜; verified). For the four CLI rows the PORT_MAP note can drop "runtime qualification pending" and keep the D64 text.

## Rows

| Row (upstream path) | Verdict | Evidence |
|---|---|---|
| `cli/experimental/cli.ts` | DONE | `TestExperimentalCLICommandComposition`, `TestCommandCompositionErrors` (group diagnostic "Expected experimental command: server or client"), `TestDevelopmentCLIEntryUpstream`. Mutation: group message and subcommand action dispatch killed. |
| `cli/experimental/command-options.ts` | DONE (D64 for the Radius/auth branches) | `TestExperimentalTransportAddressBoundaries`, `TestExperimentalCLIParserBoundaries`. Mutations killed: every diagnostic text, `unix:////`, `?`/`#`, NUL, authority, scheme-case. Two survivors are unreachable/equivalent in upstream too: the "requires an absolute path" branch (a `unix:///` path always starts with `/`) and the redundant `argument == "--"` test in option lookup. |
| `cli/experimental/command.ts` | DONE | `TestExperimentalCLICommands` (the ten upstream cases), `TestCommandExecutionWaitsAndPropagatesActionCancellation`, `TestCommandExecutionErrorsAndInvalidInput`, new `TestExperimentalCLIRejectsDuplicateOptions` (a mutation that made `--session-id` repeatable survived; every non-repeatable option and the repeatable `-e` are now asserted). |
| `cli/experimental/commands/client.ts` | DONE (D64 for `--auth-token`/`--auth-token-file`/`radius://`) | `TestExperimentalCLICommands` client rows, `TestExperimentalCLIRejectsDuplicateOptions`; mutations on `-c`/`-r` spellings, prompt detection, exclusivity and `--provider requires --model` killed. |
| `cli/experimental/commands/server.ts` | DONE (D64 as above) | Same tests, server rows; `--server-id` UUIDv4 diagnostic and `--session-dir` single-occurrence killed. |
| `experimental/services/agent-controller.ts` | DONE | `TestUpstreamAgentController` (the three upstream cases), `TestAgentControllerInitiation*`, new `TestAgentControllerRemoteClientAndViewReachProviderMembers` (all 7 members over a real Chord provider/binding and over the guarded view, value, argument and error pass-through, resolve failure), new `TestAgentControllerViewAdmissionForwardsEveryMemberArguments` and `TestRemoteAgentControllerBeginServiceMemberForwardsArguments`. Before: 12 of 14 wire-member and 5 of 7 admission-forward mutants survived; after: all killed. |
| `experimental/services/connection.ts` | DONE | Existing: `TestConnectionMatchesPinnedSourceWithLocalBindings` (executes the pinned source), `connection_*_test.go`. New `connection_fences_test.go`: attachment identity changed without an event, re-emitted identical attachment, stale transition completion neither publishes nor reports, degraded publication on a transition failure without a readiness wait, upstream diagnostics ("Client is disconnected", "…not the current attachment", "A Session is still attached"), activation runs once, announced-target snapshot, stale catalogue response. New `client_runtime_sources_adapters_test.go` covers the real client adapters (`sourceTarget`, `sourceAttachment`, `sourceCatalogue`, state replica, bindings start unbound, local services rejected). 9 of 30 mutants survived before; the remaining non-killed ones are equivalent (equal-state replacement is suppressed by `SourceState`). |
| `experimental/services/plugins.ts` | DONE | `TestPluginServiceContracts`, `TestPrepareSessionPluginsRequestJSON`, new `TestPluginRemoteClientsAndViewsReachProviderMembers` (both services, remote and view). `prepareSession` wire name was a surviving mutant in the package. |
| `experimental/services/presentation-ui.ts` | DONE | `TestPresentationUILocalViewFollowsReloadAndRevocation`; new selector tests below exercise `Select`/`ShowStatus` arguments end to end. Mutations of each forwarded argument killed. |
| `experimental/services/server.ts` | DONE (row ⬜ was stale: `internal/experimental/services/server.go` implements `createExperimentalServerServices`) | `TestServerServicesMatchPinnedUpstream` (full trace against the unmodified pinned server and Chord sources), `TestServerMutationsFIFOAndDisposeDrainFailure`, `TestServerMutationFailuresRetainDirectoryAndRecover`, `TestServerConcurrentAttachmentsRetainIndependentSelections`, `TestServerQueuedCancellationPreservesAdmission`, `TestServerDisposePreservesReleaseErrorTree`; upstream `experimental-server-lifecycle`, `-server-profile`, `-session-directory` cases via `TestPortWave08ServerLifecycle`, `…ServerProfile`, `…SessionDirectory`. 16 production mutants (revision, FIFO, draining, removal preparation, refresh-after-success, detach clearing, release message) were all killed or equivalent (`preparedPluginPackagePaths = nil` is gated by `prepared`). |
| `experimental/services/sessions.ts` | DONE | `TestServiceContracts`, new `TestSessionManagementRemoteClientAndViewReachProviderMembers` and `TestSessionDirectoryRemoteClientAndViewFollowProviderState`. |
| `experimental/services/slash-commands-provider.ts` | DONE | `TestPortWave07ExperimentalSlashCommands` (3 upstream cases), `TestBuiltinFacetReloadStopsAtFirstFailure`, `TestBuiltinCommandsPreserveArgumentsAndCancellation`, `TestBuiltinExactModelRejectsAmbiguousBareIDs`; new `TestBuiltinModelCommandCompletionsAndSelector`, `TestBuiltinThinkingCommandSelector`, `TestBuiltinCompactAnnouncesBeforeCompacting`, `TestSlashCommandRegistryNameValidation`, `TestSlashCommandRegistryPublishesOnlyVisibleChanges`, `TestSlashCommandFacetIdentities`. Before: completion text, "(selected)" labels, selector-value errors, status texts, thinking descriptions, name regex, staged-replacement publication survived; all killed now. |
| `experimental/services/slash-commands.ts` | DONE | `TestSlashCommandRegistrationAndCallbackOriginIdentity`, new `TestSlashCommandsViewRegisterAndReplaceReachTheirOwnMembers` (view `Register` forwarded to `Replace` survived). |

## Commits

- `c8a393601` remote clients, guarded views, connection fences
- `f080b1110` built-in slash command selectors and registry publication
- `a40a4dbc8` CLI duplicate options and client service source adapters

## Commands run

`go vet` (+ `GOOS=windows`, `GOOS=darwin`), `go tool golangci-lint run ./internal/experimental/ ./internal/experimental/services/` (0 issues), `go test -race ./internal/experimental/services`, `go test -race ./internal/experimental -run 'CLI|Command|Transport|ClientSource|SourceStateReplica|ClientBindingAdapter'`, `make divergence-guard`, `make source-hygiene`. Mutation harness: apply one textual mutation, run the package tests, restore (about 200 mutants over the slice files).

## Not green here and not in this slice

`go test ./internal/experimental` fails nine tests that need Node dependencies absent from the Pi 1.0.4 mirror (`typebox`, `cross-spawn`, the interface-extractor's `typescript`): `TestCoordinator*`, `TestEncodeControlLineExactUpstreamJSON`, `TestPinnedUpstreamRadiusSource`. They belong to the coordinator/Radius files, outside this slice. They fail identically on the base commit.

## Notes for review

- `services.RemoteServiceBindingOptions.Bound` is never set to true by production code; upstream always passes `bound: false`. The field is a harmless leftover and was not changed.
- The full `./internal/experimental` run takes about 130 s per pass, so mutations on `client_runtime_sources.go` were filtered to the adapter tests.

## Review (rev-gap-exp-services)

Verdict: ACCEPT-WITH-FIXES. Review commits:

- `441014d65` fixes a real gap in the `cli/experimental/command-options.ts` row. `canonicalCommandURLPath` omitted `^` from the WHATWG path percent-encode set. Under Node 24.19.0 (`.node-version`), `new URL("unix:///tmp/a^b").href` is `unix:///tmp/a%5Eb`, so Pi rejects the address with `Invalid --connect address`, and PiG accepted it. `TestExperimentalTransportAddressBoundaries` now rejects `unix:///tmp/a^b` and `unix:///^` (red before the fix). A differential sweep of 93,856 generated addresses against Pi's `parseTransportAddress` (Radius branch replaced by D64's unsupported-transport result) has zero differences after the fix.
- `cd6dbf766` makes `TestBuiltinCompactAnnouncesBeforeCompacting` prove its name: moving `ShowStatus` after `Compact` survived the old test. It also corrects upstream line citations that pointed at Pi 0.99 lines.

The nine `./internal/experimental` failures listed above are an environment omission, not missing mirror files: they pass after `make parity-deps`. With those dependencies, `go test -race ./internal/experimental ./internal/experimental/services` passes.

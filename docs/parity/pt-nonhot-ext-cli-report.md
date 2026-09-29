# PT-088–PT-091 test-porting report

## Scope and result

Slice: `pt-nonhot-ext-cli`.
Reference: Pi 0.87.1 under `.upstream/v0.87.1/`.
The case denominator below expands `test.each`: the controller has eight cases and the client TUI has three cases.
Ten files are fully ported, including the client TUI, the development CLI entry, and plugin reload promoted by the `expt-1` lane.
Five files remain pending on missing production composition.
The total is 76 of 117 cases, including the previously ported stable-entry case.
No case is newly designed out or marked live-only.
No production behavior changes, divergences, lint suppressions, or user-facing fixes are included.

All paths in the first column are under `packages/coding-agent/test/`.

| Upstream test | Cases ported / total | Disposition | Go evidence or blocker |
|---|---:|---|---|
| `git-merge-and-resolve-extension.test.ts` | 9 / 9 | ported | `coding/extension/host/subprocess/upstream_examples_test.go`, `TestGitMergeAndResolveExample` |
| `input-transform-streaming-example.test.ts` | 6 / 6 | ported | Same file, `TestInputTransformStreamingExample` |
| `plan-mode-extension.test.ts` | 4 / 4 | ported | `examples/extensions/plan-mode/main_test.go`, four `TestPlanMode*` cases |
| `plan-mode-utils.test.ts` | 33 / 33 | ported | `examples/extensions/plan-mode/utils_test.go` |
| `experimental-client-tui.test.ts` | 3 / 3 | ported | `internal/experimental/client_tui_upstream_test.go`, `TestExperimentalClientTuiUpstream` |
| `experimental-agent-controller.test.ts` | 8 / 8 | ported | `internal/experimental/services/agent_controller_upstream_cases_test.go`, `TestUpstreamAgentController` |
| `experimental-cli-entry.test.ts` | 3 / 3 | ported | `cmd/pig/experimental_entry_test.go`, `TestStableEntryDoesNotDispatchExperimentalCommands`; `cmd/pig-experimental/entry_upstream_test.go`, `TestDevelopmentCLIEntryUpstream` |
| `experimental-internal-process.test.ts` | 2 / 2 | ported | `internal/experimental/process_upstream_unix_test.go`, `TestUpstreamInternalProcess` |
| `experimental-plugin-reload.test.ts` | 1 / 1 | ported | `internal/experimental/port_wave_07_experimental_plugin_reload_test.go`, `TestPortWave07ExperimentalPluginReload` |
| `experimental-presentation-facets.test.ts` | 0 / 4 | pending | B4 |
| `experimental-radius-relay.test.ts` | 7 / 7 | ported | `internal/experimental/radius_{auth,relay,reconnect,upstream_cases}_test.go` |
| `experimental-remote-runtime.test.ts` | 0 / 26 | pending | B5 |
| `experimental-server-lifecycle.test.ts` | 0 / 3 | pending | B6 |
| `experimental-server-profile.test.ts` | 0 / 5 | pending | B7 |
| `experimental-slash-commands.test.ts` | 0 / 3 | pending | B8 |

## Assertion audit and mutation evidence

The existing plan-mode tests omitted the short-item text assertion and reused mutated completion fixtures instead of the upstream independent fixtures.
The utility tests now retain every upstream case name, command row, input, expected result, and source line.
The extension tests now assert exact host-call options and both `setActiveTools` calls.
Pi's cases are at `plan-mode-utils.test.ts:13–260` and `plan-mode-extension.test.ts:106–166`.

The input and merge tests execute the unchanged upstream TypeScript factories through PiG's production Node subprocess, Go Runner, and UIBridge.
The assertions and fake exec boundary are in Go.
These tests prove event transmission, actual file reads, exec gating, result transformation, and host-call delivery rather than registration alone.
Pi's cases are at `input-transform-streaming-example.test.ts:42–83` and `git-merge-and-resolve-extension.test.ts:63–205`.
The corresponding example bodies are `examples/extensions/input-transform-streaming.ts:18–39` and `examples/extensions/git-merge-and-resolve.ts:73–117`.

The controller tests use the concrete Chord FacetHost and production controller for catalogue and invocation assertions.
A typed Go service token is constructed from the production `AgentControllerID` because Go types and values share one namespace.
The remaining cases preserve all five admission-error rows and the exact queue, navigation, compaction, and resume arguments from `experimental-agent-controller.test.ts:44–177`.
`LaneOperation` cannot carry private harness result details; the Go boundary projects those details out before controller invocation.

The process tests launch the production coordinator through the current test executable's existing entry adapter.
They assert a distinct child PID and a listening control socket, then separately assert that immediate termination returns only after SIGKILL exit.
The `!windows` constraint matches the upstream suite's `describe.skipIf(process.platform === "win32")`, not a new exclusion.
The cases are at `experimental-internal-process.test.ts:27–48`.

The Radius audit adds the exact upstream accept/data/send/remote-close sequence, exact callback counts and error text, and routes the empty handshake error through the client factory instead of a leaf helper.
The cases are at `experimental-radius-relay.test.ts:104–285`.
Existing deterministic retry tests retain the one-second then two-second schedule and `demo-1` selection.
All gateways are explicit local values under the existing D64 boundary.

| Compiling mutation | Assertion that fails | Evidence log |
|---|---|---|
| Complete step 1 for unknown marker 99 | `TestMarkCompletedSteps/ignores_markers_for_non-existent_steps` | `plan-utils-mutation.log` |
| Plan refinement uses `steer` | `TestPlanModeQueuesRefinementAsFollowUpUserMessage` | `plan-extension-mutation.log` |
| Runner omits `streamingBehavior` | `TestInputTransformStreamingExample/skips_exec_during_steering` | `streaming-mutation.log` |
| UIBridge changes user-message delivery to `steer` | All three merge conflict-message cases | `followup-mutation.log` |
| Controller omits busy operation ID | `TestUpstreamAgentController/maps_admission_error_to_a_stable_response/lane_busy` | `controller-mutation.log` |

The first two mutations were restored before committing.
The remaining mutations use Go overlays outside the worktree.
These are sensitivity proofs of already matching production behavior, not red/green bug-fix claims.

## Specific production blockers

These blockers remain open for the integrator and owning production lanes.
Generic infrastructure tests do not replace the missing application paths.
No test-only implementation is added to manufacture closure.
The following case references are relative to `.upstream/v0.87.1/packages/coding-agent/test/`.

### B1–B3: closed

The client TUI, the development CLI entry, and worker plugin reload are ported and promoted to `ported` in `test/parity/interfaces/test-mapping-v0.87.1.json`.
The cases are `experimental-client-tui.test.ts:99–105`, `experimental-cli-entry.test.ts:47,53,60`, and `experimental-plugin-reload.test.ts:8`.

### B4: presentation facets

The server/client composition and the package profile/build/load pipeline are absent.
`experimental/plugins/package.ts` and `bundled.ts` build and dynamically import ESM facets into the process.
Current extension-runtime rules prohibit introducing an embedded or linked JavaScript runtime.
The owner must select a compliant realization before these application tests can close.
Persisted selection, cache identity, and reload behavior still have Go counterparts, so none of these cases is designed out.
Blocked cases in `experimental-presentation-facets.test.ts`:

- :32 `rejects local plugin paths for Radius servers`.
- :42 `restores plugin package selections for later server generations`.
- :53 `builds conventional plugin entries into the server-owned plugin cache`.
- :158 `builds the example plugin package without a package-owned build script`.

### B5: durable remote runtime

Go has coordinator, Radius, Chord, and local service libraries but no `startServer`, `openClientRuntime`, `runClient`, or durable Session worker-manager composition.
These cases require the complete framed-client/server/worker path, not only a local service invocation.
The faux worker makes this suite hermetic; missing credentials are not the blocker.
Blocked cases in `experimental-remote-runtime.test.ts`:

- :85 `uses PI_SERVER_DIR and PI_SERVER_ID`.
- :115 `rejects a provider without a model`.
- :121 `preserves an existing Session model when the server default changes`.
- :146 `rejects model options when discovery selects an existing server`.
- :153 `rechecks an auto-discovered server after a version mismatch`.
- :167 `serializes concurrent cold activation and retires after both clients leave`.
- :196 `passes client plugin packages to a cold server and restores them for its next generation`.
- :234 `retires a cold server after its only Session attachment disconnects`.
- :250 `runs and discovers multiple logical servers from one directory`.
- :285 `hydrates and mutates server Session services across framed clients`.
- :346 `hydrates and updates the Models service across concurrent framed clients`.
- :384 `loads conventional Session facets from multiple configured plugin packages`.
- :433 `uses the most recently selected model for a new Session`.
- :459 `composes management attachment with Session service hydration`.
- :480 `fences superseded attachment hydration by attachment generation`.
- :536 `observes keyed service instances and fences replacement generations over framed transport`.
- :592 `streams prompt events through the worker-owned service provider`.
- :629 `replicates terminal operation state after consecutive prompts`.
- :663 `stops an idle Session worker after its client disconnects`.
- :675 `starts one process per attached session and stops them during shutdown`.
- :689 `server runtime replaces an exited worker on the next attach`.
- :706 `discovers workers after replacing the server`.
- :738 `retires an unclaimed idle worker after replacement demand expires`.
- :757 `restores tracked sessions that are outside the replacement catalog`.
- :783 `reports missing and ambiguous session selections`.
- :806 `rejects a duplicate session ID within one durable repository`.

### B6: server lifetime

`experimental/server.ts` has no Go `ServerLifetime` or `startServer` caller.
A test-only timer object would not port the production boundary.
Blocked cases in `experimental-server-lifecycle.test.ts`:

- :9 `holds a foreground server until explicit shutdown`.
- :19 `retires an automatic server if no first client arrives`.
- :31 `requires both client and worker demand to disappear`.

### B7: server profiles

`experimental/server.ts` has no Go `acquireServerProfile` or server activation caller.
Coordinator socket ownership does not implement launcher locks or the `default-server-id` file.
Blocked cases in `experimental-server-profile.test.ts`:

- :23 `serializes launchers and preserves the server identity`.
- :43 `does not serialize different server IDs in one directory`.
- :58 `does not share the default identity across server directories`.
- :70 `rejects a corrupt default identity`.
- :77 `rejects an invalid explicit server ID`.

### B8: slash-command facets

There is no Go `SlashCommandRegistry`, `createSlashCommandsRuntimeFacet`, or experimental client TUI consumer.
The stable extension command registry has a different replacement and ownership contract.
Blocked cases in `experimental-slash-commands.test.ts`:

- :10 `registers and removes contributions`.
- :24 `stages replacements until the previous registration retires`.
- :41 `tracks plugin facet reload and unload`.

## Verification evidence

Evidence is retained in the lane owner's `pt-nonhot-ext-cli` evidence directory.
The full test run and the extensions parity family run in the background and retain their exit status.
No assertion, comparator, timeout, or release-policy baseline is weakened.

- Plan-mode module: `GOWORK=off go test -race -count=1 ./...`, native vet, and Windows vet pass.
- New subprocess example cases: focused normal and race tests pass.
- Experimental process, controller, and Radius packages: `go test -race -count=1 ./internal/experimental ./internal/experimental/services` passes.
- `go vet ./...` and Windows vet for every touched root-module package pass.
- `go tool golangci-lint config verify`, full `make lint`, explicit touched-package lint with `--allow-parallel-runners`, and separate plan-mode-module lint pass.
- `make ci-contracts ci-drift` stops at `test-porting-release`. The keep-going run confirms `ci-drift` and all other contract gates pass. The test inventory validates all 598 mapping entries and records seven more ported files than the committed lane base. The unchanged release policy still emits the same 359 hot-path blockers; policy-derived comparison against the base mapping proves zero newly blocked paths. These assigned batches are reviewed non-hot and do not appear in that hot-path list.
- Full `go test ./...` fails: the subprocess package reaches its default ten-minute total budget during an existing Rust fixture build; `test/ci-images/TestLinuxShardsRetainEveryCheck` finds the existing `test-porting-release` shard mismatch; `test/upstream-parity/TestRunner_NoUnclassifiedPiGMethods` finds unclassified `BindAbort`.
- `make parity-family FAMILY=extensions-runtime` compares against real Pi 0.87.1 and fails four existing scenarios: `24-tool-renderers-collapsed` and `25-tool-renderers-expanded` differ in assistant-text left padding; `27-command-argument-completions` differs in footer model/auto display; `21-extension-tool-and-command-info` differs in its captured API artifact.
- `git fetch public main` succeeds. Default `make lint-changed` cannot find a merge base with the local `main`; the fetched `public/main` does have a merge base. `make lint-changed LINT_BASE=public/main` then fails because it passes the standalone plan-mode module to the root workspace. Explicit root-package lint and independent `GOWORK=off` plan-mode lint both pass; the lint-changed driver remains a tooling blocker.

The broad-suite failures remain integrator blockers, not accepted flakes or passing evidence.
The lane changes no shared production files.

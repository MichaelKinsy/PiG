# port-99-f6a: family 6, lane 6A (settings, session persistence, session lifecycle messages)

Scope and rules: `docs/plan/progress/family-6-split.md` section 3 (lane 6A). Base: staging `porter/pi-0.99.1` at 15904a765. Branch `port-99-f6a` on the lane's team namespace.

## Lead answers applied

- Q4: a header-only session file (an empty file that `Open` initialised, which upstream 0.99.1 also writes) loads as flushed, exactly as upstream 0.99.1 `_setSessionFile` does (`.upstream/v0.99.1/packages/coding-agent/src/core/session-manager.ts:1035-1051`). The first appended entry goes to the file directly and the header is not written twice. `TestHeaderOnlySessionFileResumesAsFlushed` proves it.

## Ported tests (red commit)

| upstream test file | Go test | cases |
|---|---|---|
| `test/settings-manager.test.ts` | `internal/codingagent/settings_manager_upstream_test.go` (`TestUpstreamSettingsManager`) | deviceId (1), fullscreen wheel scroll lines (1), defaultTools +name/-name (3) |
| `test/default-tools-setting.test.ts` | `coding/session_tool_registry_port_test.go` (`TestDefaultToolsInitialSelectionPort`) | activates an inactive extension tool with +name (1, pending on `defaultActive`, see below) |
| `test/session-manager/file-operations.test.ts` | `internal/codingagent/session_file_operations_upstream_test.go` (`TestSessionFileCreationUpstream`) | 3 new |
| `test/session-manager/tree-traversal.test.ts` | `internal/codingagent/session_tree_traversal_upstream_test.go` (`TestCreateBranchedSessionUpstream`) | 2 changed |
| `test/suite/agent-session-runtime.test.ts` | `coding/session_runtime_replacement_upstream_test.go` (`TestRuntimeOriginalUnflushedForkDiagnostic`) | message text |
| `test/session-file-invalid.test.ts`, `session-id-readonly.test.ts`, `startup-session-name.test.ts`, `stdout-cleanliness.test.ts` | `cmd/pig/*_upstream_test.go` | citations moved to upstream 0.99.1 lines; the file-URL substitution has no Go counterpart (per-file comment) |

Existing Go tests that asserted the old text or the old file-creation rule were changed to the upstream 0.99.1 rule: `coding/session_ops_test.go`, `cmd/pig/rpc_default_session_dir_test.go`, `internal/codingagent/w3_sessions_test.go`, `internal/codingagent/deferred_flush_test.go`, `test/parity/scenarios/session/12-unsaved-clone-fork.toml`.

Additional regression tests (not upstream): `internal/codingagent/session_persistence_0991_test.go`, `internal/codingagent/settings_0991_test.go` (the conversation rule over poisoned histories, crash before the first reply, header-only resume, the merge and resolve rules of `defaultTools`, the wheel setter, an unparsable global file, `codemode` nesting).

## Stubs added in the red commit

`Settings.FullscreenWheelScrollLines`, `Settings.DeviceID`, `Settings.Codemode` (`CodemodeSettings`, `CodemodeMode`), `WheelScrollLines`, `SettingsManager.GetFullscreenWheelScrollLines`, `SetFullscreenWheelScrollLines`, `GetOrCreateDeviceID`, `GetSettings`, `Session.EntryCount`. Other lanes read these and add none.

## Red run

`docs/plan/evidence/port-99-f6a.red.txt`.

## Green

Implementation:

- `internal/codingagent/session.go`: the conversation gate. `Session.hasConversation` replaces `hasAssistant`; `AppendEntry` opens it for a `message` entry with role `user` or `assistant`, and the first such entry flushes the header and every buffered entry (`session-manager.ts:1166-1185`). `session_restore.go` and `session_manager.go` (Clone) use the same rule for a restored or cloned branch (`session-manager.ts:1717-1725`). `Session.EntryCount` (`session-manager.ts:1511`).
- Unsaved-session message: `coding/runtime_replacement.go`, `internal/codingagent/session_selectors.go` (`agent-session-runtime.ts:313`).
- `internal/codingagent/settings_default_tools.go`: `mergeDefaultTools` and `resolveDefaultTools` (`settings-manager.ts:222-245`). `Settings.GetDefaultTools` and `SettingsManager.GetDefaultTools` return the resolved list. The two startup readers of the raw field, `cmd/pig/cli_runtime_build.go` and `cmd/pig/rpc_mode_runtime.go`, now call the resolver (`sdk.ts:264-269`); this is a one-line edit in files that another lane owns.
- `internal/codingagent/settings.go`: wire, clone, merge and accessors for `fullscreenWheelScrollLines`, `deviceId` and `codemode`; `GetOrCreateDeviceID` (`settings-manager.ts:1172`), `GetFullscreenWheelScrollLines` and the setter (`1386-1399`), `GetSettings` (`564`).
- `internal/codingagent/bug_report.go`: bug reports drop `deviceId` as well as `trackingId` (`bug-report.ts:62`); one line in a file outside the lane's list.
- Docs: `docs/site/docs/{sessions,sdk,settings}.md`, `internal/pigdocs/content/{sessions,settings}.md`. Changelog: `changelog.d/port-99-f6a-settings-session.md`.

PiG tests whose premise the upstream rule changed (not ported upstream tests) were rewritten in the green commit: `TestForkUnsavedRefusalPreservesSource` (`w3_sessions_test.go`; a user message now creates the file, so only a setup-only session is unsaved), `TestClonePreservesPersistenceModeAndDefersConversationFreeBranches` (`session_clone_runtime_test.go`; a user-only clone is written at once), the "not written yet" export case (`session_export_test.go`). The ported upstream tests were not edited.

Added in green: `cmd/pig/rpc_default_tools_modifiers_test.go` (startup selection through the RPC process), `TestBugReportOmitsDeviceID`. Both were red before the fix they cover (the bug-report test was run against the unfixed code; the RPC test was not red-proven, see mutation checks).

## Refactor and evidence

- The first flush now opens the file exclusively (`createSessionFile`, `openSync(file, "wx")`, `session-manager.ts:1175`): a file that appeared at the session's path is reported as `EEXIST` and left untouched instead of being truncated. `TestFirstFlushDoesNotOverwriteAnExistingFile` was red before the change (it saw no error and a truncated file). The user-message rule makes this path much more common than the assistant rule did, which is why it was fixed here.
- Mutation checks (15 mutations, all red after the two survivors were fixed by new tests: a CLI-path selection test and a codemode project-mode merge case): `docs/plan/evidence/port-99-f6a.green.txt`.
- Load test: `internal/codingagent` with `-race -count=24`, `GOMAXPROCS=4`, `taskset -c 100-103` and 8 busy-loop burners on the same cores, over the session-file, clone, settings, device-ID and concurrency tests: PASS (same evidence file). `TestConcurrentFirstMessagesCreateOneCompleteFile` and `TestGetOrCreateDeviceIDIsAtomic` cover the concurrent first flush and first ID creation.
- Package tests with `-race`: `./internal/codingagent/ ./coding/ ./cmd/pig/` (results below in the READY report).

## Known red until the pin moves

- `TestW3SessionPiComparison` (`internal/codingagent/w3_pi_test.go`) compares the unsaved-fork error with the real published 0.87.1 package, whose text still says "Wait for the first assistant response". It goes green when the comparator moves to 0.99.1.
- `test/parity/scenarios/session/12-unsaved-clone-fork.toml` now expects the 0.99.1 text on both sides; it is red against the 0.87.1 oracle until the pin moves. Other session scenarios that compare file creation timing against the 0.87.1 oracle need the same oracle move (not run here: no 0.99.1 oracle).
- Unchanged failures that also fail on the base commit: the `TestRPC33*` oracle-version guards (`coding`), the Mistral, pi-messages and Bedrock RPC replays and the two stdout-backpressure tests (`cmd/pig`), and every Rust or Python SDK test on this host (the mise shims for Python fail; the packages `test/extension-conformance` and `coding/extension/host/subprocess` were not green here for that reason).

## Pending on other lanes

- `default-tools-setting.test.ts` "activates an inactive extension tool with +name": the Go case registers the tool without `defaultActive: false` because the field arrives with lane 6D's contract. Add `DefaultActive: false` to the `registryTool` in that case when the contract lands; the case then proves the `+name` override of an inactive tool.
- `coding/services.go` (6D) should alias `WheelScrollLines` and `CodemodeSettings` next to `Settings` if the SDK surface wants to name them.
- Integrator: `pig-go.json`, `recommendations-*.json` (new exported symbols above), the PORT_MAP rows for `settings-manager.ts`, `session-manager.ts` and `agent-session-runtime.ts`, the test-mapping hashes for the ten upstream test files in this lane, and the `docs/extension-api-parity.md` row for `getSettings`.

## Per-case notes

- Malformed `defaultTools` values (a non-array, or non-string items): upstream keeps them in the merged object and `getDefaultTools` filters strings (`settings-manager.ts:1430-1434`); PiG drops a mistyped key or array item at load time like every other settings array (`decodeSettingsWire`). No test in upstream 0.99.1 covers it.
- `session-file-invalid`, `session-id-readonly`, `startup-session-name`, `stdout-cleanliness`: only the harness `--import` argument changed (file URL instead of path). The Go tests start the compiled binary directly, so the substitution has no Go counterpart; each file carries a comment with the upstream lines.

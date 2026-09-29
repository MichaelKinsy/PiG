# Pi directory opt-in: 0.3.0 evidence

## Decision and contract

`PIG_USE_PI_DIRS=1` explicitly selects Pi's agent and project directories. Default PiG directory identity remains unchanged (D2). The switch is an environment variable because selecting a settings file from a setting inside that file is circular.

| Surface | Default | Shared mode |
|---|---|---|
| Agent directory | `PIG_CODING_AGENT_DIR`, else the PiG configuration root plus `agent` | `PI_CODING_AGENT_DIR`, else `~/.pi/agent` |
| Project resources | `<cwd>/.pig` | `<cwd>/.pi` |
| Session override | `PIG_CODING_AGENT_SESSION_DIR` | `PI_CODING_AGENT_SESSION_DIR` |
| Explicit session path | `--session-dir` wins, then the selected environment variable, then `sessionDir` settings | Same |
| PiG-owned SDK caches, documentation and user Piglets | PiG configuration root | Unchanged |

Only the exact value `1` enables sharing. Shared mode ignores the two PiG agent/session overrides. It does not merge trees, copy credentials, install a symlink or disable project trust. `PIG_CODING_AGENT_DIR=~/.pi/agent` alone already relocated agent files and sessions, but did not select project `.pi`, did not configure external adapters and did not make the old auth/trust locks interoperable.

The reference is [Pi 0.87.1](https://github.com/earendil-works/pi/tree/v0.87.1), with its [coding-agent documentation](https://github.com/earendil-works/pi/tree/v0.87.1/packages/coding-agent/docs). Paths follow `packages/coding-agent/src/config.ts:528-573`; default session storage follows `core/session-manager.ts:589-597`. Settings locking and changed-key writes follow `core/settings-manager.ts:241-296,547-593`; auth locking, permission preservation and mutation follow `core/auth-storage.ts:24-25,69-199,472-515`; trust locking follows `core/trust-manager.ts:133-171`.

## Root causes fixed

- Project paths used constant `.pig` joins even when the agent directory pointed at Pi. Configuration, discovery, trust, source metadata, system prompt files and the Node configuration exports now share the selected directory identity.
- Auth, trust and the dynamic catalog cache used OS file locks, which Pi's `proper-lockfile` cannot observe. Pi's `core/models-store.ts:55` uses the same `FileAuthStorageBackend` as credentials. All three now use Pi's mkdir/mtime lock-directory protocol. Settings uses the same implementation, including locked reads. Authentication modifiers retain their lock through callback completion and refuse a cancelled or compromised commit.
- Auth writes replaced the inode and forced mode 0600 on existing files. Pi writes in place and applies the mode only at creation. PiG now preserves existing permissions, ACLs and symlink targets.
- OAuth serialization omitted empty `access`, `refresh` and zero `expires`. Pi's `ReadOnlyAuthStorage` requires all three fields (`auth-storage.ts:258-267`). PiG retains them.
- The new project prompt probe exposed missing headless `getSystemPrompt` bindings. Print, JSON and RPC now bind the Session's actual prompt rather than the host fallback empty string, matching `core/agent-session.ts:3124`. Those host reads require synchronization with the agent worker: the agent reads prompt text and presence under its message lock, and the Session publishes its baseline atomically. Empty projected prompts remain distinct from an absent prompt.

## Red and green evidence

| Guard | Before the fix or compiling mutation | Final assertion |
|---|---|---|
| `TestPiDirectoriesOptIn` | Agent and project paths remain `.pig`; Pi override ignored | Default/off values remain separate; exact opt-in selects `.pi`, including a tilde override; product state remains separate |
| `TestPiDirectoriesProjectSettingsAndSessions` | Reads conflicting `.pig` theme `dark` | Reads `.pi` theme `light`, writes only selected project settings, uses the shared session root |
| `TestAuthStorageUsesPiLockDirectory` | A regular lock file is visible and remains after the operation | A lock directory spans the modifier and is removed afterward |
| `TestFileModelsStoreUsesPiDirectoryLock` | Leaves a regular model-cache lock file that Pi cannot acquire | Catalog reads/writes use the shared lock; cancellation while waiting cannot commit later |
| `TestAuthStoragePreservesExistingMode` | Existing mode is replaced by 0600 | Existing mode survives the credential write |
| `TestOAuthCredentialZeroFieldsRemainReadableByPi` | All three required fields are absent | All three fields survive serialization; real Pi's read-only reader accepts them |
| `settings/10-pi-directories-opt-in` | Compiling mutation `UsePiDirs() => false` prints `PIG-PROJECT`, misses the extension artifact and fails the comparator | Three real-Pi pairs: exact `SHARED-PROJECT` output and equal structured path/settings/resource artifact |
| Same scenario, headless prompt binding | PiG records `system:false`; Pi records `system:true` | Both record the project append prompt; `TestHeadlessExtensionGetsConfiguredSystemPrompt` covers the binding directly |
| `TestSystemPromptReadDuringHistoryAndOverrideChanges` | Race detector reports concurrent prompt/history reads and writes | Prompt read and presence use one lock; `TestSystemPromptSnapshotDistinguishesAbsentAndEmpty` preserves the empty/absent boundary |
| `TestSettingsReadHonorsPiWriterLock` | Compiling mutation removes the read lock and parses partial JSON | Read refuses the held lock, then reads the complete settings after release |

The initial scenario fixture incorrectly used `.mjs` for auto-discovery. Pi auto-discovers `.ts` and `.js` (`core/extensions/loader.ts:658`), so the fixture now uses `.ts`. An initial artifact environment path also collided with the runner's relative-path expansion; the final fixture uses a command argument. Neither failed harness attempt is counted as a PiG defect. The heartbeat test initially read its mtime without the mutex; the race detector caught that test bug, and the final test uses the same mutex as the heartbeat. The permission test now explicitly applies `chmod(0660)`, as Pi's test does, instead of letting the worker's umask change its input. Mutation sources use `.go.txt` filenames so repository-wide Go commands do not mistake them for a package.

## Real shared-file round trip

`TestPiSharedFilesRoundTrip` writes settings, API-key credentials with scoped environment and provider-defined fields, an expired OAuth credential and a session through PiG. It imports the installed Pi 0.87.1 implementations, asserts their version, reads those files, writes back settings/auth/session changes, and loads the results through PiG. PiG preserves Pi's provider-defined auth field when writing an unrelated credential. Both parse the same custom-model configuration. `models.json` remains unchanged: neither application has a settings-style writer for this file, so this is a common-reader test, not a fabricated application-write claim.

`TestPiSharedFilesConcurrentWriters` runs Pi and Go concurrently against one agent directory. Each performs 32 distinct credential, dynamic catalog and trust writes, while both update separate settings keys. Every requested credential, catalog and trust key survives, both final settings values survive, an unknown PiG settings object survives, and no lock directory remains. Pi also rereads every catalog that PiG wrote. The operation count is an input, not an observed-output snapshot.

Additional tests cover stale locks, compromised ownership, cancellable acquisition, a cancelled active modifier retaining its lock until its callback returns, and heartbeat cleanup. The upstream test mapping remains partial where independent obligations remain, notably coalesced auth reads and queued in-memory cancellation. This change does not claim full AuthStorage async parity.

## Real corpus packages

Replay script: `test/parity/testdata/pi-directories-corpus.mjs`.

- Powerline is `pi-powerline-footer@0.18.0` from the release corpus. Its npm tarball matches the copied source files. Integrity: `sha512-lVzhreltNGubR2xbUrwUbIpxMZfhQHhSCP2LacnQ+QOUQUXDyuRW5SKW+cqF/c2gyyPG9zry2a9DiSD3R2FSjA==`.
- The adapter is `pi-acp@0.0.34`, copied from the A1/A2 investigation's locked install.
- Each run uses a new scratch HOME and cwd, no live credentials, offline mode, an explicitly trusted scratch project and the existing test-faux provider. Pi loads the checked-in provider extension; PiG uses its built-in test provider. The one directory opt-in is passed to PiG and to the adapter. No `PI_CODING_AGENT_DIR` override is needed.

| Probe | Pi 0.87.1 | PiG shared mode |
|---|---|---|
| Persist a print session, then open Powerline's actual welcome | `recent-session (just now)` | Same escaped recent-session row |
| pi-acp global `/shared-global ARG` | `GLOBAL-ARG` | `GLOBAL-ARG` |
| pi-acp project `/shared-project ARG`, with conflicting `.pig` template present | `PROJECT-ARG` | `PROJECT-ARG`, never `WRONG-ARG` |
| pi-acp command inventory | Descriptions from both Pi templates | Same, despite conflicting `.pig` templates |
| Native `session/list` | Contains current session ID | Contains current session ID |
| Adapter restart and mapped `session/load` | Resumes and answers `MAPPED-RESUME` | Same |
| Move only the scratch map aside, restart and load by native discovery | Resumes and answers `NATIVE-RESUME` | Same |
| Adapter map location and recorded backend file | `.pi/pi-acp/session-map.json`, `.pi/agent/sessions/...` | Same |

This closes the directory assumptions identified in pi-acp A1/A2 and the Powerline recent-session row, not every package/UI difference. Powerline's initial token estimate remains 667 for Pi versus 646 for this PiG probe; those raw values are retained, not normalized. The headers and provider-fixture inventory differ as expected. No corpus process remains after cleanup.

Reproduce from the worktree with a locked Powerline npm install and pi-acp install:

```bash
go build -o bin/pig ./cmd/pig
PATH="$HOME/flakes3-tools:$PATH" node test/parity/testdata/pi-directories-corpus.mjs \
  bin/pig extensions/sdk-ts/node_modules/.bin/pi \
  /path/to/powerline/npm /path/to/pi-acp/dist/index.js \
  "$(mktemp -d)"
go test ./internal/codingagent -run '^TestPiSharedFiles' -count=1 -v
```

Raw logs, panes, ACP JSONL, identities and hashes are retained under `tmp/pi-dir-optin/corpus-release/`. The script prints the separate scratch-home root. No checked-in fixture or original npm installation is modified. The tested binary SHA-256 is `738cd789efcabd934139e88b438a7deb87ea4ad6528bd54fe1d4de9fce394e76`. The local evidence archive is `tmp/pi-dir-optin/evidence.tar.gz`, SHA-256 `19a2ee76f00d2b74ad43fd309aad6832faa99c465e72f5ce5e4056254aa08bad`.

## Safety boundary

Sharing is not a transactional multi-writer session editor. Run independent sessions concurrently, not two writers of one JSONL session. Use one pi-acp adapter per home because its session map has its own adapter-owned write policy. Do not edit auth/settings/trust files manually while either application is writing them. Pi's in-place JSON writes do not provide crash-atomic replacement. Keep a backup before opting into an existing tree.

Auth uses Pi's synchronous acquisition limits and 30-second asynchronous acquisition/stale contract. Its owned heartbeat keeps long modifiers live, cancellation does not release a still-running callback's lock, and a compromised lock refuses a commit. A regular auth/trust/model-cache lock file from an older PiG build is rejected rather than unlinked under a potentially live old process. Stop all users of the store before removing that regular file. Do not remove an active lock directory.

A custom `PI_CODING_AGENT_DIR` works for host resources and sessions, but cannot relocate package-hard-coded paths. pi-acp's template loader still hard-codes `~/.pi/agent/prompts` and project `.pi/prompts`. PiG does not rewrite the adapter's code or map. PiG-only Package declarations, extensions and Piglets do not become supported in Pi merely because the JSON parser accepts unknown settings keys.

## Resource and performance evidence

On Linux amd64, Go 1.27.1, Intel Xeon 6746E, `BenchmarkLockRoundTrip` measured 55.0–58.3 microseconds/op, about 1376 B/op and 15 allocations/op across three samples. CPU profiling is dominated by filesystem syscalls (68%); allocations come from lock ownership, stat/path conversion and the ticker. This is a file-operation cost, not a rendering-path benchmark or a speedup claim. Each lock owns one heartbeat goroutine and ticker; release joins it and removes the owned directory. No lock retains a file descriptor between operations. The cancellation and release tests cover that lifetime.

```bash
go test ./internal/pilock -run '^$' -bench BenchmarkLockRoundTrip -benchmem -count=3 \
  -cpuprofile=lock.cpu -memprofile=lock.mem
go test -race ./internal/pilock ./ai ./internal/codingagent \
  -run 'TestLock|TestCancelledAcquisition|TestAuthStorage|TestPiSharedFiles|TestPiDirectories|TestSettingsReadHonorsPiWriterLock|TestSettings.*Lock'
```

The headless prompt getter also has a before/after benchmark and CPU/allocation profiles. Before synchronization it measured 277 ns for one message and 3.89 microseconds for 4096 messages. After synchronization, three samples measured 242–264 ns and 4.00–4.03 microseconds respectively, with unchanged 224 B/op and four allocations. This is not a speedup claim. The getter scans the transcript's system-message references without copying the full history; headless host callbacks run off the TUI loop. The agent's read lock and the Session's atomic baseline avoid racing the active agent worker.

## Gates

| Check | Result |
|---|---|
| Touched production packages' tests, including CLI and subprocess suites | Pass; the complete CLI and subprocess package runs take about 270 and 285 seconds |
| Focused race tests for shared stores, lock lifetime, prompt reads and baseline publication | Pass |
| Real Pi round trip and concurrent writes | Pass |
| Real Powerline/pi-acp replay | Pass; exact recent-session ANSI row, both advertised/executed templates, and replayed history after both resume paths |
| `make parity-family` for settings, project-trust, print, RPC and model-runtime-store-catalog | All pass at declared durability |
| `go vet ./...` | Pass |
| `GOOS=windows go vet` for touched production packages | Pass |
| `GOOS=windows go vet -tags=parity ./test/parity/runner` | Blocked by inherited `test/parity/runner/main_test.go:42` using Unix-only `syscall.Kill`; the changed runner environment guard adds no platform-specific code |
| `go tool golangci-lint config verify`, touched-package lint, `make lint` | Pass |
| `make lint-changed LINT_BASE=fdcf667708` | Pass; this worktree has no local `main` ref, so the task's base is explicit |
| `go fix -diff ./...` | Empty |
| Coverage, Go interface and recommendation regeneration | Current |
| `make ci-contracts` | Blocked by inherited pending/partial hot-path upstream test entries at `test-porting-release` |
| `make ci-drift` | Blocked by inherited unapproved D77 and D78 at `divergence-quality` |
| Remaining drift/contract targets: divergence guard, source hygiene, docs drift, format-version inventory, factory ledger, SDK surface | Pass; obsolete `AI-29` lock-loop baseline entry removed with its source |
| `go test ./...` and `go test ./test/parity/...` | All touched packages pass; three inherited closure tests fail because they assume all 33 divergences are approved |

The inherited closure failures are `TestImportCurrentDenominatorsAccountsEveryLedgerRow`, `TestDivergenceDashboardMatchesCurrentHeadingsAndScrutiny`, and `TestFoundationDashboardAccountsCurrentDenominatorsWithoutCredit`. The base branch marks D77 and D78 `SCRUTINIZED:pending`, so its actual approved/provisional count is 31, not the tests' 33. The first full run also found stale coverage after adding the scenario; regeneration resolved that failure. This lane does not approve pending divergences, change hot-path tags, lower the ported baseline or weaken those tests. These are integration-owner release blockers, not evidence that the sharing implementation passed every repository gate.

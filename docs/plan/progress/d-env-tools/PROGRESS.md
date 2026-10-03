# d-env-tools: pi-durable src/env and src/tools

Branch `team/smc1/d-env-tools`, merged with the staging `d-foundation` lane (`8e3785cec`).

## Status

READY for review (`5b7498c8c` and later), with the partial chord-guide row and the unported examples listed under "chord-guide and examples".

## Reassignment (superseded)

The lead handed `tools/*`, `env/node.ts` and `storage-benchmark.ts` to `sg-durable-tools`, then told this lane to finish its remaining scope (env/index, truncate, chord-guide, examples) to green. The finished tools and env work stays on this branch; sg-durable-tools should merge it. `storage-benchmark.ts` was never started here.

## HANDOFF

### Done (green, `go test -race ./durable/env ./durable/tools`)

- `durable/env` (`node.go`, `exec.go`, `line_reader.go`, `decoder.go`, `paths.go`, `shell.go`, `errors.go`, `process_*.go`): NodeExecutionEnv implementing the d-foundation `env.ExecutionEnv` declarations in `env.go` (not edited). Added over the 0.99.2 `agent/harness/env` base: `TruncateFile`, `FlushFile`, `Id()` `node:local`, `SetCwd`, raw `OnOutput` streaming with a per-stream streaming UTF-8 decoder (TextDecoder semantics: BOM dropped at stream start, one U+FFFD per maximal subpart), spill thresholds (`AfterBytes`/`AfterLines`, partial last line counts), spill path on timeout/abort errors, spill failure error, line reader BOM drop.
- `NodeExecutionEnv.Self`: methods call `Self.CreateTempFile/FileInfo/OpenTextLineReader/CreateTempDir`, so an embedding type that overrides one sets `Self` to itself (TypeScript subclass dispatch). Used by the ported tests.
- `OnOutput` has no error return (d-foundation decision); a panic in it is the upstream throw and becomes `callback_error`.
- Tests ported from `env-node.test.ts` (`filesystem_test.go`, `shell_test.go`) and `env-node-spill.test.ts` (`spill_test.go`), upstream titles as names. Extra non-upstream tests are marked in comments (BOM, partial-line threshold, decoder flush).
- `durable/tools`: read, write, edit, bash, image detection, path utils, file mutation queue, edit-diff/diff/patch (copied from `internal/codingagent/tools`, the 0.99.2 port of the same jsdiff pipeline; `normalizeForFuzzyMatch` now uses `jsstring.TrimEnd` and `countOccurrences` follows JS `split("")`). Typed against `durable.ToolRegistration`/`ToolExecutionApi`. `tools_test.go` ports all of `tools.test.ts`.
- Mutation-checked: queue wait, queue key (file system id), not_found handling, symlink kind, timeout message, offset, `@` prefix, spill thresholds, decoder flush, callback error, spill path on interruption.
- `env-truncate.test.ts` is already ported by d-foundation (`durable/truncate_upstream_test.go`); no work here.

### Decisions / notes for reviewers

- Test seams (package vars, not parallel-safe): `syncFile`, `readFileAt`, `writeSpillChunk` in `durable/env`. They replace upstream's `vi.spyOn(FileHandle.prototype, ...)` and the `node:fs` `createWriteStream` mock.
- Not ported, Go cannot express them: `truncateFile` sizes 1.5/NaN/Infinity (`int64` parameter; -1 and MAX_SAFE_INTEGER+1 are tested); the `process.platform = "win32"` mock in the legacy-WSL case (transport depends only on the path) and the taskkill async spawn error case (Go's `Start` returns that error synchronously and `killProcessTree` callers discard it). The win32-only detached-descendant case runs on every platform with a background `sleep`.
- `CreateDirOptions.Recursive` is a `bool` in the d-foundation declaration, so `&CreateDirOptions{}` is non-recursive; upstream `{}` means recursive. Only a nil options value defaults to recursive. Flag to d-foundation if a caller needs `{}`.
- Merged the staging `d-harness-b` lane (which holds foundation, storage, session, harness-a); `tools/read.go` now uses `harness.CharacterEnd`. `go build ./durable/...` and `go vet` pass with the env and tools packages. `durable/storage/sqlite` has one failing test of its own (`TestPicoSqliteStorage` corrupt-operation message), not from this lane.
- `durable.FormatSize` uses `jsstring.ToFixed` (good); `durable.TruncateHead` is d-foundation's.
- Bash tool: `OnOutput` calls `api.Output(text)`; `api.Output` panicking after the invocation ends becomes a `callback_error`.

### chord-guide and examples (this session)

- `durable/harness/chord_guide_test.go`: all three cases of `chord-guide.test.ts` against a real Harness, MemoryStorage and the attached Session state. The row is `partial`: the Chord facet host, `defineService`/`defineFacet`, the remote service binding and the tracked-Harness shutdown order have no Go form, because `internal/chord`'s facet host is built on the 0.99.2 pico3 state types and has no bridge for an attached Session state (chord files `facets/host.ts`, `services/loopback.ts` are unstarted). The file is tagged hot-path, so it blocks `make test-porting-release` until that lands.
- `durable/examples/*_test.go`: examples 00-15, 17-20 and 30 as Go tests (`TestExample00Conversation` ... `TestExample30ToolOverride`), asserting what each script prints. Example 17 and 30 run the real coding tools (`CodingTools`, `CreateBashTool` with a prefix, a `wrapTool` timing wrapper) through the Harness; 17 over JSONL storage; 13 across a SQLite close and reopen.
- Not ported, with reasons: 16 (needs a live model; no credentials here); 19's `--ops` mode (`Conversation.Watch` is a stub: `view.go` waits on session observation exports) and `events` mode (`watchEvents`, `harness/events.ts`, not in `durable/harness`); 17's timing hook (`hook(ToolTask, ...)` only prints); 21 late-join, 22/23 subagents, 24 child tasks, 25 compaction, 26 coding-agent, 27 plan mode, 28 reviewer, 29 sandbox per conversation, 31 reload and restart: not started, they use harness features (subagent spawn, compaction, hooks) still landing in d-harness-c and the other harness lanes.

## Red to green

- Env: written first against `env-node.test.ts`/`env-node-spill.test.ts` titles; the first runs were green because the 0.99.2 `agent/harness/env` base already behaved the same, so each risk got a mutation instead: pending-spill guard in `drain` (spill test red), spill thresholds (`&&` to `||`, partial-line count; 3 tests red), spill path on interruption, callback error, decoder flush (stdout and stderr), BOM at stream start. Mutations that survived at first (partial-line count, decoder flush, stderr flush) got the extra tests marked "not upstream" and then went red.
- Tools: queue wait removed (hang, 3 tests red), queue key without file system id (red), `not_found` handling (red after adding the canonical-failure test), symlink/directory kind check (red after adding the not-a-file test), timeout message, read offset, `@` prefix, command prefix (examples 30 red).
- Examples: examples 09 and 14 first failed on my guessed expectations (JSON escaping of `<`; the system entry follows the user entry); the assertions now state the observed, upstream-comment-consistent behavior.

## Next

1. sg-durable-tools: merge this branch for `tools/*` and `env/node.ts`.
2. When `facets/host.ts` and the attached-state service bridge land, add the facet-host steps to `chord_guide_test.go` and flip the row to `ported`.
3. Port examples 21-29, 31 and 19's watch modes as the harness lanes land `Conversation.Watch`, `watchEvents`, subagents and compaction.

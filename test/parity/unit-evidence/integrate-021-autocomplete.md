# Integration autocomplete command priority

## Contract and cause

Pi 0.87.1 assembles builtin commands, prompt templates, extension commands, then skills in `packages/coding-agent/src/modes/interactive/interactive-mode.ts:747-776`. `packages/tui/src/autocomplete.ts:335-341` strips `skill:` when scoring an unqualified query. Equal scores preserve insertion order. PiG assembled skills before extension commands, so `/btw` selected `skill:btw` instead of the package's `btw` command.

The installed Pi `CombinedAutocompleteProvider` probe returns `["btw","skill:btw"]` for Pi's order and the reverse for PiG's old order. The fix changes only the host catalog assembly order. It does not alter fuzzy scoring, command dispatch, or skill expansion.

## Red and green

- `TestExtensionCommandPrecedesSameNameSkillAutocomplete` fails before the fix: the suggestions are reversed and Tab accepts `/skill:btw ` instead of `/btw `. It drives the production catalog builder and editor input path.
- `autocomplete/08-extension-command-before-skill` fails before the fix against real Pi: Pi executes the command and emits `AUTOCOMPLETE COMMAND SELECTED`; PiG expands the skill and sends it to the provider. It passes three paired runs after the fix with escaped equality.
- The first scenario attempt used a skill path outside the copied cwd and failed on both hosts. That attempt is not red evidence. The corrected scenario copies the fixture cwd and names its `SKILL.md` explicitly.
- The complete autocomplete and extensions-runtime families pass after the fix. No existing comparator is weakened.
- Full Linux vet, Windows vet for `internal/codingagent`, lint configuration verification, changed-file lint, full lint, `make ci-contracts ci-drift`, and `go test ./internal/codingagent ./test/parity/...` pass. The first scenario-lint run rejected the missing `Upstream evidence` comment label; the comment is corrected.

Raw logs and profiles are under `tmp/integrate-021/autocomplete-*` in the integration worktree. The failing paired capture is `test/parity/artifacts/08-extension-command-before-skill/20260926T201227Z`.

## Resource disposition

The fix moves one existing loop. It adds no worker, retained cache, handle, IPC, or per-keystroke history access. Catalog construction remains at startup/reload/settings rebuild; fuzzy lookup uses the constructed catalog. No speedup is claimed.

`go test ./internal/codingagent -run '^$' -bench '^BenchmarkAutocompleteCommandOrder$' -benchmem` measures construction plus one query on Go 1.27.1, Linux amd64, Intel Xeon 6746E:

| Extension commands and same-name skills | ns/op | B/op | allocs/op |
|---:|---:|---:|---:|
| 0 | 16,370 | 10,506 | 27 |
| 8 | 24,790 | 18,472 | 44 |
| 512 | 715,967 | 698,558 | 607 |

CPU and allocation profiles use the same workload. The separate `pi-btw` independent `createAgentSession` limitation is not fixed by this change.

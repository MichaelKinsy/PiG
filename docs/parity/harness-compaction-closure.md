# Durable harness compaction test closure

## Contract and representation

Pi 0.87.1 `packages/agent/src/harness/compaction/compaction.ts` stores retained messages on the compaction entry. It does not use coding-agent `firstKeptEntryId`. `agent/harness/compaction` implements the harness contract and `agent/harness/agentharness` uses its preparation/result types for compaction hooks.

Preparation expands the latest compaction's retained messages into temporary entries before choosing a cut point. It preserves the previous summary and file details. It derives token usage from the current harness projection. The harness heuristic does not count native system messages and prefers a nonzero provider `totalTokens` value.

The Go helpers return values and errors instead of TypeScript `Result` wrappers. Failed and aborted assistant responses return the existing `harness.CompactionError` codes and messages. Request errors propagate unchanged. Context parameters carry cancellation and telemetry separately from `ai.StreamOptions`. Each summary request receives its own routing identity and disables cache writes. History and turn-prefix requests run sequentially; a failure prevents the later request.

Summary conversion does not apply provider normalization to the conversation being quoted. Failed assistants and orphaned tool calls remain visible to the summarizer. Normalizing them first lost failed attempts and inserted synthetic tool results.

The file-operation accumulator is shared with coding-agent compaction. It sorts file names by UTF-16 units, as JavaScript does. Summary serialization remains separate because the harness catches JSON-stringification failures while coding-agent does not. The full utility source remains partial in `docs/parity/PORT_MAP.md`: the original test file does not prove complete argument insertion-order or isolated-surrogate behavior. Its `ported` test disposition applies to its 22 cases, not those additional source facets.

## Evidence

- `TestHarnessCompactionUpstream` ports all 11 preparation/context/serialization cases.
- `TestHarnessCompactionGenerationUpstream` ports all 11 generation cases and every reasoning/error row.
- `TestHarnessSummaryRequestOwnership` proves cancellation and telemetry ownership and prevents a prefix request after a rejected history request.
- `TestHarnessSummaryKeepsUnnormalizedHistory` first failed because a failed assistant vanished and an orphaned call gained `No result provided`. The raw conversion fixes both effects.
- `TestHarnessCompactionUsesModelRuntime` drives the public helpers through the actual Services-owned Model Runtime and verifies provider request options, instructions, tools, usage, and routing isolation.
- `TestCompactionFileListsUseJavaScriptSort` first failed on private-use and supplementary-plane names. The shared UTF-16 ordering fixes the stock coding-agent caller too.
- `test/parity/scenarios/compaction/14-harness-compaction-retained-tail.toml` compares preparation, complete summary request text/options, retained data, file details, combined usage, routing separation, coded errors, and poisoned history against unchanged installed Pi. It uses exact output equality and three pairs.

Compiling mutations remove retained-tail expansion, remove output caps, reuse routing identity, replace the aborted code, or restore provider normalization. The unit guards and paired scenario fail. No assertion is skipped or normalized.

## Resource disposition

Preparation performs bounded linear work over the supplied path and retained messages. It owns only temporary slices/maps and does not add a task or cache. Each generation request is awaited. Retry behavior uses the existing `ai.RetryAssistantCall` owner.

The 2,000-entry `BenchmarkPrepareHarnessCompaction` measures roughly 0.66–0.74 ms, 1.06 MB, and 2,046 allocations on this host. Most allocation remains in projection/result slices; returning message values rather than pointers did not materially change the allocation count. Reproduce the profiles with `go test ./agent/harness/compaction -run '^$' -bench '^BenchmarkPrepareHarnessCompaction$' -benchmem -cpuprofile=cpu.pprof -memprofile=mem.pprof`; the named unit tests and canonical scenario reproduce the behavioral evidence.

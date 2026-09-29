# Session selector search

## Contract

Pi 0.87.1 is the oracle. `packages/coding-agent/src/modes/interactive/components/session-selector-search.ts:23-27,39-203` searches ID, name, `allMessagesText`, and cwd. The query parser splits JavaScript whitespace, retains slash characters, supports quoted phrases, and falls back to plain whitespace tokens for unbalanced quotes. Phrases normalize whitespace on both sides. Fuzzy tokens call the shared `packages/tui/src/fuzzy.ts:16-95` matcher on the original text. Every token must match. Scores add together.

Empty queries and Recent mode retain incoming order. Threaded and Fuzzy modes rank nonempty queries by score, then modified milliseconds descending, with stable ties. `session-selector.ts:389-416,637-679` retains and clamps the selected index as results change. Enter selects that row. The initial selection is zero; query edits do not force it back to zero.

## Findings

PiG substituted substring positions and a fixed token-length score for fuzzy matching. It also sorted Recent results again and normalized only the text side of phrases. Query tokenization omitted several JavaScript whitespace characters. The shared fuzzy matcher counted UTF-8 bytes, treated NEXT LINE as whitespace, missed BOM word boundaries, and used simple lowercase conversion instead of context-sensitive Unicode lowercase conversion.

The selector now calls the shared fuzzy matcher, normalizes only phrase text, preserves incoming order where Pi does, and uses stable millisecond tie-breaking. The shared matcher scores UTF-16 units and uses Unicode lowercase conversion. The fix adds no background task, IPC, filesystem access, cache, or retained history copy. The existing selector index policy remains unchanged.

## Regression evidence

`TestUpstreamSessionSelectorSearch` reads `test/parity/scenarios/session/testdata/search-cases.json`. Its first ten rows preserve all nine original upstream case sites and their ten ordered-result assertions, including all four name-filter cases. Supplemental rows check fields, ordering, fuzzy scoring, alphanumeric swaps, and whitespace. Each row exercises the pure function and the production picker, including Enter and the empty-result case.

`TestSessionSearchRetainsAndClampsSelection` types `delta` through the production input handler after selecting the first, second, or last row. It asserts the retained/clamped index and the exact selected path. `TestSessionSearchQueryTokens`, `TestSessionSearchScores`, and `TestSessionSearchStableTiesAndInputOwnership` test parser boundaries, numeric scores, UTF-16 positions, stable ties and nonmutation. `TestFuzzyMatchUnicodeScores` compares numeric scores independently measured from published Pi.

The task supplied five names but no original IDs, message histories, cwd, or JSONL files. The DD1 fixture reconstructs `alpha one`, `bravo`, `charlie`, `delta four`, and `golf seven` and obtains Pi's two-result `delta four`, `alpha one` order. That row already passed before the fix; it does not prove the original WSL failure by itself. The distinguishing scoring row retains a valid fuzzy match in `golf seven` and makes the pre-fix order `delta four`, `golf seven`, `alpha one`, while Pi returns `delta four`, `alpha one`, `golf seven`. This row fails before the fix, including Enter after selecting the second row. No name-specific exclusion hides a valid fuzzy match.

`20-session-selector-search` compares production search results, selected paths, and Unicode scores with the real installed Pi package using `output_equal`. `21-session-search-resume-picker` launches both real CLIs with `--resume`, reads five JSONL fixtures, types `delta`, and compares the complete escaped picker block using `escaped_output_equal`. Both scenarios declare three pairs and apply no output normalization. Future fixture timestamps make the age column deterministically `now`.

Both scenarios fail behaviorally with a compiling overlay of the two pre-fix production files. The terminal failure preserves the swapped rows in raw and escaped captures. Both pass with the corrected implementation. Raw captures, mutation overlays and profiles are retained outside the checkout with the change's verification handoff.

## Resource measurements

`BenchmarkSessionSelectorSearch` exercises refiltering across 10 and 1,000 in-memory summaries. Three Linux/amd64 samples measured 19–22 microseconds, about 9.6 KB and 87 allocations for 10 summaries; 1.82–2.01 milliseconds, about 1.02 MB and 8,015 allocations for 1,000 summaries. CPU and allocation profiles accompany the logs. Unicode lowercase transformation is the largest allocation site. These measurements characterize this fixture, not a speedup or a bound for arbitrarily large histories. Search still scales with the loaded searchable text and number of query tokens; this change does not redesign the picker loader or scheduling.

## Verification

The full `internal/codingagent` and `tui` packages, focused race tests, native repository vet, Windows vet for both touched packages, lint configuration verification, changed-file lint, and full repository lint pass. The complete Session parity family passes. Startup resume, project-trust resume selection, settings theme filtering, and both model fuzzy-filter cross-probes pass at declared durability.

`make generate`, scenario lint, PORT_MAP drift, coverage drift, interface drift, source hygiene and docs drift pass. The aggregate `ci-contracts` gate remains blocked by existing pending/partial hot-path tests. The aggregate `ci-drift` gate remains blocked by D78's missing approval. The repository-wide `go test ./...` run also encounters unrelated Python shim failures, the default conformance-suite timeout while building Rust fixtures, and existing D78 closure-dashboard failures. No timeout, retry, skip, normalization or baseline relaxation is added to hide these failures.

This change closes the original upstream search test file. It does not claim exhaustive JavaScript regular-expression syntax equivalence or close unrelated picker storage, deletion, tree ordering, or asynchronous loading behavior.

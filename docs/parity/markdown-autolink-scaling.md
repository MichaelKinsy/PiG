# Markdown autolink scaling

## Contract and source

Pi 0.87.1 uses `marked` 18.0.11 (`packages/tui/package.json:56`). `packages/tui/src/components/markdown.ts:303-329` lexes the complete source and wraps the rendered lines. The pinned dependency's `marked/lib/marked.esm.js:14` defines its inline URL, email, and text rules; `:44` emits link/text tokens and `:58` dispatches them. Ordinary text is consumed as a text token instead of testing every remaining suffix independently. `markdown.ts:686-710` renders links, suppresses duplicate bare URL/email fallback text, and emits OSC 8 when supported.

This change preserves the existing Go recognition and styling rules. It does not claim completion of the partial Markdown port or introduce new syntax. `TestAutoLinkScannerMatchesMarked` compares text, href, and rune boundaries with the exact vendored dependency. The corpus includes empty and ordinary text, Unicode prefixes, punctuation, multiple `@` characters, URL parentheses, multiple links, and the reported 64 KiB paragraph. The existing Links tests preserve the upstream inputs from `packages/tui/test/markdown.test.ts:1587-1697`. The new precedence tests exercise the production inline-render path after code and bold tokens.

## Root cause and lifetime

`renderInlineMarkdown` visits runes individually. Its former `parseAutoLink` scanned to the next whitespace, converted the whole remaining word to a string, trimmed it, and ran an anchored email regex on every visit. Both CPU and suffix allocations grew quadratically. Trailing URL parentheses also repeatedly recounted the same prefix.

A render-local scanner now records the next word boundary and possible email suffix once. A successful URL consumes its candidate once. Prefix lookahead remains bounded by the longest recognized scheme. Parenthesis trimming counts unmatched closers once. The scanner owns four integer offsets, retains no source string, starts fresh for each inline source, and creates no worker, global cache, size cap, or output truncation. The transform cache, revision, queue, and Dispose paths are unchanged.

## Red and green evidence

Toolchain: Go 1.27.1, Node 24.19.0, Linux amd64, Intel Xeon 6746E. All runs use private HOME, Pi/PiG agent directories, and TMPDIR.

`TestMarkdownUnbrokenLinearScaling` runs cold `NewMarkdown(strings.Repeat("x", size)).Render(80)` calls. It checks complete retained text, a one-second ceiling, and allocation growth below 2.5x when input doubles. The allocation ratio avoids a timing-ratio assertion that depends on CPU scheduling. Repeated benchmarks independently measure time scaling.

| Input | Before, cold render | After, cold render | Before allocations | After allocations |
|---|---:|---:|---:|---:|
| 16 KiB | 5.374 s | 3.282 ms | 154,135,216 B | 2,753,224 B |
| 32 KiB | 29.785 s | 7.981 ms | 603,388,288 B | 5,893,248 B |
| 64 KiB | 134.103 s | 17.663 ms | 2,361,657,208 B | 12,082,432 B |

Five 100 ms benchmark samples give median cold-render times of 3.735, 7.510, 15.071, and 33.494 ms for 16/32/64/128 KiB. The standalone 64 KiB autolink scanner allocates zero bytes for ordinary text and repeated invalid URL prefixes. Invalid emails, repeated `@`, and long trailing-parenthesis inputs also complete in linear scans. CPU/allocation profiles move the remaining cost to wrapping and allocation rather than suffix matching.

Canonical scenario `tui-components/13-markdown-unbroken-autolink-scaling` compares every rendered row of the complete 64 KiB paragraph with the installed Pi component, using `output_equal`, no normalization, and three pairs. A compiling overlay of the original production file makes this scenario fail its unchanged 30-second process budget. The original regression test fails both the one-second and doubling assertions. Scenario `05-markdown-text-fits` now uses `escaped_output_equal` instead of normalized equality.

## Verification

Commands:

```sh
go test ./tui/... -race -count=1
go test ./tui/... ./test/parity/testdata/markdown-unbroken-pig -count=1
go test ./tui -run '^$' -bench 'Benchmark(AutoLinkScanUnbroken|MarkdownUnbroken)$' -benchmem -count=5 -benchtime=100ms
go test ./tui -run '^$' -bench '^BenchmarkMarkdownUnbroken$' -benchtime=3x -benchmem -cpuprofile parse.cpu -memprofile parse.mem
make parity-family FAMILY=tui-components
make parity-family FAMILY=interactive-rendering
go vet ./...
GOOS=windows go vet ./tui/... ./test/parity/testdata/markdown-unbroken-pig
go tool golangci-lint config verify
go tool golangci-lint run ./tui/...
go tool golangci-lint run --build-tags parity ./test/parity/testdata/markdown-unbroken-pig
make generate
make coverage
go run ./test/parity/cmd/gointerfaces -out test/parity/interfaces/pig-go.json
python3 test/parity/probes/normalization_inventory.py
make -k ci-contracts ci-drift
```

TUI tests, race tests, fixture tests, native/Windows vet, lint, and the complete tui-components family pass. The interactive-rendering family has one unrelated failure: `37-auto-theme-dark-modal-notification` cannot find `Pick a color` on either Pi or PiG. Rebuilding with the original Markdown production file reproduces the same failure on both sides. Other family scenarios pass, including assistant/user text, LaTeX, Mermaid, and resumed thinking Markdown.

After merging stable integration `fbcc3a7d5`, the complete TUI race suite, native/Windows vet, touched/tagged lint, tui-components family, and focused cross-family Markdown scenarios pass again. Scenario 13 completes all three Pi pairs.

The post-merge repository gates remain blocked by 217 pre-existing upstream-test release findings and D78's missing approval (the initial base had 218 findings). Scenario lint, coverage drift, source hygiene, documentation drift, and the remaining contract/drift checks pass after local generation. Generated coverage, interface, and normalization files are left to the integrator as required by the lane contract. No divergence or lint suppression is added.

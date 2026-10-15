# PiG fork of yuin/goldmark v1.8.5 (ast, parser, text, util)

These four directories are the non-test sources of `github.com/yuin/goldmark/{ast,parser,text,util}` v1.8.5, copied from the module proxy (MIT, `LICENSE`). They are packages of the root module (`github.com/MichaelKinsy/PiG/third_party/goldmark/...`); a `replace` directive would make `go install github.com/MichaelKinsy/PiG/cmd/pig@latest` fail. Only the packages the Mermaid fence, heading, definition and HTML parsers of `internal/codingagent` import are copied.

The `parser` and `util` packages compiled eleven regular expressions in package initializers, so every process that linked them paid about 160 KB and 1,500 allocations at start whether or not it parsed Markdown. The fork compiles each expression on first use.

## Differences from v1.8.5

1. Import paths inside the copied files point at the fork.
2. `parser/lazy.go` and `util/lazy.go` (new): `lazyRegexp(expr)` returns a `sync.OnceValue` that compiles `expr` on its first call.
3. `parser/raw_html.go` (`openTagRegexp`, `closeTagRegexp`), `parser/html_block.go` (`htmlBlockType1OpenRegexp` through `htmlBlockType7Regexp`) and `util/util.go` (`emailDomainRegexp`) hold `lazyRegexp` values and call them where the code used the compiled value. The expressions are unchanged.
4. `util/unicode_case_folding.go`: `unicodeCaseFoldings` is a `sync.OnceValue` that builds the case-folding map on first use instead of in `init` (about 80 KB at process start); `util.go` calls it where the code read the map.
5. Tests, the root package, `extension`, `renderer`, `testutil` and `go.mod` are not copied. `test/docs-drift` still uses the upstream module for its link extraction.

## Updating

Copy the four directories of the new release over these, repeat the differences above and run `go test ./internal/codingagent -run Mermaid`. Offer nothing upstream without the owner.

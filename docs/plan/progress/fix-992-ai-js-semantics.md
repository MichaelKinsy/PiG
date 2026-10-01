# fix-992-ai-js-semantics: JS-semantics gaps in ai (audit-992-ai findings F4 and F5)

Base: `staging/porter/pi-0.99.1` at `94f45865e`. Oracle source: `.upstream/v0.99.2` (Pi 0.99.2).

## F4: Retry-After Date.parse formats

Already fixed on the base tip. `ai/js_date_parse.go` ports V8's DateParser and `providerRetryDelay` (`ai/provider_retry.go`) calls `jsDateParse` for the `retry-after` fallback (`provider-retry.ts:58-63`). The audit red test `TestAudit992RetryAfterAcceptsDateParseFormats` (UTC zone, one-digit day, `+0000`, no weekday) passes on the tip without a change. No format is missing, so no code change. The test is kept as a regression guard.

## F5: strict `require` reason key order

Cause: `makeJSONSchemaNodeStrict` printed the unsupported keyword's value with `jsonStringifyValue`, which encodes Go maps with sorted keys. Pi prints `JSON.stringify(value)` (`constrained-sampling.ts:65-70`), which keeps authored order (array-index keys first).

Fix at the root: the authored order is already retained by `schemaObjectOrder` (`ToolSchema.UnmarshalJSON` decodes with an ordered token reader). The reason now serializes the keyword through `marshalSchemaWithOrder` at its schema path, so nested objects keep source order and index keys enumerate first. `marshalSchemaWithOrder` now writes keys and scalars with `marshalJSONUnescaped` (no HTML escaping, like `JSON.stringify`). `jsonStringifyValue` had no other caller and is replaced by `marshalJSONUnescaped`. A non-finite number renders `null` as `JSON.stringify` does.

## Commits

1. `b8fe2fa80` test(ai): port Pi 0.99.2 Retry-After Date.parse and strict-require reason tests (red). Run: `TestAudit992StrictRequireReasonKeepsObjectKeyOrder` fails (`{"a":1,"b":2}` vs `{"b":2,"a":1}`); `TestAudit992RetryAfterAcceptsDateParseFormats` passes (see F4).
2. fix(ai): print strict-require keyword with JSON.stringify key order (green). Adds `TestStrictRequireReasonStringifiesLikeJSON` (nested, index keys, no HTML escaping, sorted source).

## Evidence

- Mutation: passing a nil order to `marshalSchemaWithOrder` in the reason fails `TestAudit992StrictRequireReasonKeepsObjectKeyOrder` and three subtests of `TestStrictRequireReasonStringifiesLikeJSON`.
- `GOMAXPROCS=4 go test -race -count=24 ./ai -run 'Strict|Audit992|RetryAfter|ProviderRetry|ToolSchema|Schema'`: ok.
- `go build ./...`, `go vet ./ai`, `GOOS=windows go vet ./ai`, `go fix -diff ./ai`, gofmt of touched files: clean.
- No exported API change, so no generated file was regenerated.

## Blocked or pre-existing

- `go tool golangci-lint ./ai` reports one goimports finding at `ai/openai.go:1084`, a file outside this slice (also listed by `gofmt -l ai`). Not touched.
- `go test -race ./ai/... ./agent/...` and `./coding/...` have failures that need the Pi 0.99.2 oracle (`extensions/sdk-ts/node_modules` is absent: Pi npm package cooldown) or Node lockfile/spawn oracle tests. None involve this change. `make parity-family FAMILY=providers-faux-streaming` cannot run for the same reason (missing `node_modules/.bin/pi`).
- F1, F2, F3, F6 belong to `fix-992-federation-auth`, not this lane.

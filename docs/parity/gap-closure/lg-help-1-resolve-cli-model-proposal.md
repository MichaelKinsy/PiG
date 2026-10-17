# ResolveCliModelResult.model: proposal for a numbered divergence (needs owner approval)

Row: `pkg:coding-agent/.#ResolveCliModelResult::property:model` (T9t: Go `codingagent.RuntimeModel` has no `api`).

Pi: `ResolveCliModelResult.model` is the registry's `Model<Api>` (`model-resolver.ts:385`), so `main.ts` passes it straight on.

Go: `ResolveCliModelResult.Model` is `*RuntimeModel`, the composed-catalog selection entry (provider, id, name, reasoning, headers). `cmd/pig/startup_model.go` then builds the request model with `buildModelFromRef` (virtual models, the authenticated native binding, models.json entries).

Why Go does not carry `*ai.Model` through resolution:

- `ModelRegistry.RuntimeModels` is the startup selection path. Its doc comment states that it avoids materializing the request-only fields of `ai.Model`.
- Measured on the built-in catalog (`BenchmarkRuntimeModelsSelection` against a temporary benchmark that calls `GeneratedModel.ToModel` and `ToCapabilities` for every listed model, `-benchtime 200x`, GOMAXPROCS=8):

  | path | time/op | B/op | allocs/op |
  |---|---:|---:|---:|
  | RuntimeModels (today) | 0.77 ms | 844 KB | 1490 |
  | full `ai.Model` for every model | 5.47 ms | 1901 KB | 15081 |

  The resolver reads only provider, id, name and reasoning, so the extra work is a pure startup cost.
- An earlier attempt (`8c35595dfa`, reverted by `1100601b02`) added `Model *ai.Model` beside `RuntimeModel`. Reviewers rejected it as caller-free and as fabricating an `ai.Model` from id and name.

Options for the owner:

1. Approve a numbered divergence: the resolver returns the selection entry, and the request `ai.Model` is built after selection. Remove-when: the startup path materializes `ai.Model` lazily per resolved entry only (a resolver that returns the entry plus a lazy model constructor).
2. Approve the refactor: `ModelResolverRuntime.GetModels` returns catalog `*ai.Model`, with the cost above on every startup. Five production callers and about 15 test runtimes change.

No code or ledger row changes ride on this file.

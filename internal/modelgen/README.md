# Model catalog generator

Port of Pi's `packages/ai/catalog/**` (the catalog generation model that replaced `packages/ai/scripts/generate-models.ts`).
`go run ./cmd/gen-models` without `-src` fetches the upstream catalogs and writes the built-in model catalog the way Pi's
`node catalog/generate.ts` does, byte for byte:

- `-data-output <dir>`: the provider data files (`<provider>.json`, model values grouped by API) and `.manifest.json` (Pi's
  `src/providers/data`);
- `-json-only -json-output <dir>`: only the public catalog JSON served by pi.dev (`models.json`, `models.all.json`,
  `providers.json`, `providers/<id>.json`, `providers/<id>.all.json`);
- `-strict` fails instead of degrading when upstream data is unexpected, `-data-only` regenerates only the providers already in
  `-data-output`, `-pretty` indents, `-record <dir>` stores every upstream response, `-replay <dir>` serves recorded responses
  instead of fetching.

`go run ./cmd/gen-models -src <models.generated.ts or .js> -out ai/models_generated.go` is the other job: it turns Pi's generated
catalog into the Go catalogs of package `ai` (`make model-catalogs`, run by `make pin-bump`).

## Layout

| Path | Pi counterpart | Contents |
| --- | --- | --- |
| `obj.go` | (JavaScript objects) | `Obj`, an insertion-ordered plain object: the generated JSON is compared byte for byte with Pi's, so models keep the member order Pi's code gives them. |
| `models_dev.go`, `source_openrouter.go`, `source_vercel.go`, `sources.go` | `catalog/sources/` | Upstream fetchers: models.dev v2 (normalized), OpenRouter, Vercel AI Gateway, NVIDIA NIM, Radius. All sources are fetched before anything is built. |
| `providers/<id>.go` | `catalog/providers/<id>.ts` | One file per built-in provider; register new files in `providers/index.go`. |
| `family_*.go` | `catalog/families/` | Facts about a model family that hold on every host. They apply to every provider's models. |
| `define.go` | `catalog/define.ts` | `ProviderDefinition` and the helpers provider files use. |
| `build.go` | `catalog/build.ts` | Runs the provider definitions in a fixed order. |
| `reasoning.go` | `catalog/reasoning.ts` | models.dev effort values to Pi thinking levels. |
| `modeldata/` | `scripts/model-data.ts`, `hydrate-model-catalog.ts` | Data manifest, structure hash and validation of the data directory. |
| `../../cmd/gen-models/generate.go` | `catalog/generate.ts` | Flags, staging, validation and output. |

## Build order

For each provider: `Models` returns its chat models (upstream entries mapped to pi models, corrections, authored models;
`ctx.ProviderModels(id)` returns another provider's models from this step); `Compat` defaults for each API sit under each model's
own compat, then the shared rules run (models.dev effort levels for APIs that send them natively, then the family rules);
`Finalize` applies provider-specific behavior and wins over the shared rules; image input limits get the default resize profile,
then `FinalizeAll` runs cross-model logic. Match the TypeScript object construction exactly (member order, `||` against `??`,
where a spread puts a key): a model that is built differently serializes differently.

## Checking a change

`test/parity/testdata/catalog-generation/equivalence.sh` runs Pi's generator (from `.upstream/current`) and this one over the
same recorded upstream responses and compares the data directory and the public JSON byte for byte:

```sh
test/parity/testdata/catalog-generation/equivalence.sh record build/catalog-fixtures
test/parity/testdata/catalog-generation/equivalence.sh compare build/catalog-fixtures
```

`go test ./cmd/gen-models -run TestCatalogGenerationMatchesPi` holds the Go generator to the sha256 of what Pi's generator wrote for
a pruned set of recorded responses (`cmd/gen-models/testdata/catalog-generation`). After a pin bump, re-record, prune
(`prune.mjs`), and refresh the golden file with `equivalence.sh golden`.

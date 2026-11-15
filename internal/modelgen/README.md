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
  instead of fetching;
- `-record-source <dir>` fetches live and, when the run succeeds, replaces the catalog source in `<dir>` (`responses.tar.gz` and
  `meta.json`: the committed recording the repository's catalogs are made of, `automation/gen/catalog-source`);
  `-replay-source <dir>` runs offline over a catalog source and writes the manifest `generatedAt` the source's fetch time
  (`make model-catalogs-live` records, `make model-catalogs` replays).

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

## Released clients

Pi (`packages/ai/catalog/README.md`, e675a0d7f): pi.dev serves each published catalog to every released pi at or above
`MINIMUM_PI_VERSION` (`scripts/model-catalog-minimum-version.mjs` in Pi's repository root). Released clients use it within hours,
without updating pi, and combine it with their own code's defaults, such as `detectCompat()` in `src/api/openai-completions.ts`
(`DetectCompat` in `ai/openai.go`).

A catalog change must therefore work with the latest release's code, not only with the code in the same commit. Example: a new code
default makes `supportsDeveloperRole: false` redundant for a provider, so the line is removed from the catalog. Released clients still
default to `true` and start sending the `developer` role, which the provider rejects.

If a catalog change needs new code, either keep the catalog working with old code (add explicit values; remove nothing old clients
rely on), or set `MINIMUM_PI_VERSION` to the next release in the same commit. Clients below the minimum keep the last catalog
published for them and receive no further catalog updates. Pi's `npm run check:model-catalog-compat` only catches type errors against
the latest release, not changed behavior.

A released pig is such a client of the catalog its own endpoint serves (`internal/codingagent/remote_catalog_provider.go`, D64): for
each built-in provider it requests `/api/models/providers/<id>` from pi-in-go.dev with the `pig/<version>` user agent (D65), and a
catalog whose `Last-Modified` is later than the bundled data's generation time replaces the bundled models of the same type and id
and adds the others. So the rule above holds for every catalog that endpoint publishes: it must work with the code of the released
pig binaries.

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

The golden file holds the sha256 of Pi's generator at the pin, recorded from the mirror, except the twelve Qwen Token Plan files and
the two lists of every model (`catalog/models.json`, `catalog/models.all.json`): those are what Pi's generator writes with `catalog/providers/qwen-token-plan.ts` and `qwen-token-plan-individual.ts` of Pi 1397070024
(the current gateways, `skipDeprecated`, the Individual allowlist with `deepseek-v4.1-flash` and `deepseek-v4.1-flash-1017`) in place
of the pin's. `PI_CATALOG_OVERLAY=<dir> equivalence.sh golden ...` copies the files of `<dir>` (paths relative to Pi's `packages/ai`)
over the mirror copy before it generates.

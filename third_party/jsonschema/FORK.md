# PiG fork of santhosh-tekuri/jsonschema v6.0.2

This directory is `github.com/santhosh-tekuri/jsonschema/v6` v6.0.2, copied unchanged from the module proxy (Apache-2.0, `LICENSE`), and is a package of the root module, `github.com/MichaelKinsy/PiG/third_party/jsonschema` (a `replace` directive would make `go install github.com/MichaelKinsy/PiG/cmd/pig@latest` fail, and embedders would ignore it).

The module compiled the metaschemas of every draft (22,326 allocations, 1.7 MB, about 2 ms) in a package `init()`, so every process that linked it paid that cost at start, validator or not. The fork compiles each draft's metaschema the first time a schema of that draft is checked against it. PiG's tool parameters have no `$schema`, so a session compiles only the default draft's, 2020-12.

## Differences from v6.0.2

1. `draft.go`: `func init()` becomes `(*Draft).compileMeta()`, which compiles one draft's metaschema and vocabulary schemas with its own Compiler, once, through the new `metaOnce` field. `dialect.getSchema`, the only reader of a metaschema, calls it first. `allVocabs` holds only the vocabulary names (`map[string]struct{}`, its values were the compiled schemas) and the new `vocabSchemas` the compiled vocabulary schemas, set once by `compileMeta`: `getVocabs` reads `allVocabs` while another goroutine may be compiling the draft. Adds the `sync` import.
2. `loader.go`: `loadMeta` is a variable, so that `lazy_meta_test.go` can count the metaschema documents a process loads.
3. `go.mod`, `go.sum`, `go.work` and `go.work.sum` are not copied: the package builds with the root module's dependencies (`golang.org/x/text`; `dlclark/regexp2` for its tests). The import paths `github.com/santhosh-tekuri/jsonschema/v6` and `.../v6/kind` in the package, its tests and its importers (`ai/validate.go`, `coding/piglet/schema.go`) are `github.com/MichaelKinsy/PiG/third_party/jsonschema` and `.../kind`.
4. `.github/`, `.gitmodules`, `.golangci.yml` and `.pre-commit-hooks.yaml` (upstream repository tooling) are not copied.
5. `lazy_meta_test.go` (new): nothing is compiled at start; in a new process, validating the read tool's arguments compiles only the 2020-12 metaschema and loads its 9 documents; each draft compiles once; parallel first uses of every draft pass under `-race`; the errors and messages of a table of valid and invalid schemas and instances over every draft, the `$schema`-less default and custom vocabulary metaschemas equal `testdata/lazy_meta_golden.txt`, recorded with every metaschema compiled in `init()`, in a new process and after other tests. `BenchmarkFirstToolValidation` measures the first validation in a new process per iteration (`-benchtime=1x -count=10`).
6. `util.go`: `equals` compares a number with a number only. In v6.0.2 a numeric value on the left parsed whatever was on the right with `big.Rat.SetString`, so `1.5` equalled the string `"1.5"` (and `"0x10"` equalled 16, `"1/2"` equalled 0.5): `const`, `enum` and `uniqueItems` accepted or rejected instances that the JSON Schema comparison keeps apart. `equals_number_test.go` (new) pins it.
7. `validator.go`: an `items` error after `prefixItems` names the element's own index (`evaluated+i`), not its index among the elements after the prefix. `items_location_test.go` (new) checks it.

Every path that reads a metaschema goes through `dialect.getSchema`, which compiles the draft's first, so no code observes an uncompiled metaschema. `compileMeta`'s Compiler loads only json-schema.org documents, which `addRoot` does not validate, so it never reaches `getSchema` for the draft it compiles (in v6.0.2 that would have dereferenced the nil metaschema in `init()`).

## Updating

Copy the new upstream release over this directory, then repeat the differences above. Rewrite the import paths as in 3, run `go test ./third_party/...` (also with `-race -run 'ConcurrentFirstUse'`) and the root `ai` and `coding/piglet` validation tests. Offer nothing upstream without the owner.

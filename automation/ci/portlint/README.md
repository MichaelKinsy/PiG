# portlint

`make port-lint` runs deterministic `go/analysis` passes that flag the ways a Go port drifts from the TypeScript it maps. Each pass has a seeded bad example, a near-miss good example, and the incident that motivated it in `checks/testdata/src/<check>/`.

The driver (`main.go`, `run.go`) loads the module with its tests, runs every pass, drops findings under `//portlint:allow`, and compares the rest with `baseline.toml`. The baseline is a ratchet: a finding it does not list fails, and an entry with no matching finding fails, so a fix deletes its entry in the same change. Never add an entry for new code.

```sh
make port-lint                                   # the gate (also in make check and ci-drift)
go run ./automation/ci/portlint -report          # counts per check and every finding
go run ./automation/ci/portlint -print > baseline.toml   # regenerate; only to delete fixed entries
go test ./automation/ci/portlint/...
```

`vettool/` builds the same passes as a `go vet -vettool` for an editor or a one-package run. It prints every finding and ignores the baseline:

```sh
go build -o /tmp/portlint-vet ./automation/ci/portlint/vettool
go vet -vettool=/tmp/portlint-vet ./ai/...
```

Allow one finding at its line, or the line above, with a reason. A marker with no reason is itself a finding, and a marker names one check (or `all`):

```go
//portlint:allow mapkeyorder writeTrustFile sorts the keys before stringifying (trust-manager.ts:126-135)
```

The passes skip `/test/`, `/automation/`, `/examples/`, `/internal/evals`, `/durable/harness` and `/internal/modelgen`, which never feed Pi-facing output. The test-hygiene passes (`hardtmp`, `rawsymlink`, `unixsocketpath`, `testisolation`) scan them. The driver runs the default build and a second pass with the `integration`, `live` and `parity` tags, as `make lint` builds them.

## Checks

The JSON checks (`mapkeyorder`, `nilslicenull`, `jsonescape`) cover `encoding/json` and its fork `extensions/sdk/json`, the shared host/Go-SDK codec.

| check | severity | flags | incident |
|---|---|---|---|
| `mapkeyorder` | HIGH | `json.Marshal`/`Encode` of a Go map: Go sorts keys, `JSON.stringify` keeps insertion order | #165 edited tool input sorted keys |
| `emptydrop` | MED | `omitempty` on a slice or map field: drops an explicit `[]` or `{}`. A `json.RawMessage` field is exempt: an encoded `[]` or `{}` is two bytes and survives `omitempty` | MCP empty titles; ClientCapabilities empty objects |
| `nilslicenull` | HIGH | `json.Marshal` of a slice-typed value that nothing checks for nil: nil becomes `null`, Pi sends `[]` | Piglet empty active-tool list |
| `jsonescape` | MED | `string(json.Marshal(...))`, `json.MarshalIndent`, and encoders without `SetEscapeHTML(false)` | `JSON.stringify` never escapes `<`, `>`, `&`; settings.json, trust.json and auth.json were written with `\u0026` |
| `utf16units` | MED | `len(s)`, `s[i]`, `s[a:b]` against a limit or position in a file with a `// Ports packages/...ts` comment | truncation, cursor and width on non-ASCII text |
| `regexfold` | LOW | `(?i)` in `regexp` or `lazyregexp` patterns | gap-untested O2 |
| `numbers` | MED | integer division, float-to-int conversion and `strconv` parsing in ported files | `parseInt`/`Number`/`Math.floor` edge cases |
| `clock` | LOW | `time.Now()` in a package that declares a `func() time.Time` seam | durable reservation order; fake-clock tests |
| `asyncorder` | HIGH | `go` statement with no join in a file ported from an upstream file whose `test/parity/async-contracts.toml` contract is `microtask-ordering` | TaskAbort `setTimeout(0)` precondition; durable reservation order |
| `golifetime` | MED | `time.After` in a loop, a looping `select` with no stop case, a goroutine with no context, WaitGroup, errgroup or channel | catalog refreshes after Close |
| `gorecover` | MED | fan-out goroutine with no deferred `recover` | websearch fan-outs |
| `doubleclose` | MED | `close` of a field channel in a function with no Once, lock or CAS guard | double close flake |
| `ctxmapping` | MED | `context.Background()`, `http.NewRequest`, `exec.Command` inside a function that has a context | `AbortSignal` not mapped to context |
| `godropserr` | MED | `go f()` where `f` returns an error | a rejected Promise must surface |
| `httprequestreuse` | HIGH | assignment to a parameter request's `Body` in `RoundTrip`, or a request re-sent from a loop with no Clone or GetBody | `ai/provider_retry.go` retry rewrote `req.Body` |
| `transportperrequest` | LOW | `http.Transport` literal or `Clone()` outside a constructor (report only) | audit H1 |
| `erroridentity` | MED | `err.Error()` compared or searched as text | error classes lost when a message is reworded |
| `hardtmp` | LOW | a literal `/tmp` path handed to an `os` filesystem call | scoped-TMPDIR and Windows tests |
| `pathseparators` | LOW | `path.Join` and friends, or `strings.Split(s, "\n")`, in a file that reads or writes files | Windows paths and CRLF files |
| `rawsymlink` | LOW | `os.Symlink` in a test that builds for Windows (folds the `internal/testenv` rule) | Windows tests without the symlink privilege |
| `unixsocketpath` | MED | a unix socket in a function that builds paths under `TempDir` | socket paths over 104 bytes |
| `providerliteral` | MED | a built-in provider ID compared as a literal outside that provider's own file | Copilot-only special cases in shared paths |
| `testisolation` | HIGH | `os.UserHomeDir`, `os.Setenv` of a home or agent-directory variable, or a partial `t.Setenv` set before starting a child, in a package whose `TestMain` does not call `testenv.ScopeTempDir` | 2026-10-06 `auth.json` wipe; #163 |

Not covered by a pass, because another gate owns it: hand-edited generated files (`make generate` drift gates and `automation/ci/check-generated.sh`), the runtime agent-directory guard (`automation/ci/agent-dir-guard.sh`), and `errorlint` and `bodyclose` (golangci). Unbounded channel growth is not statically decidable and has no pass.

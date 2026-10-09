# Writing a rule family

Read the package comment in `rules.go` first. The contract is: a rule accepts a mapping only when the Go shape is provably equivalent under the rule, refutes only when provably not, and otherwise returns `ok=false`. No scores. No map-order, clock, network or random input.

## Where your code goes
Only in your family file (`naming.go`, `optionality.go`, `unions.go`, `functions.go`, `placement.go`, `datashapes.go`) and its `<family>_test.go`. Do not edit `rules.go`, `engine.go` or another family's file. If you need a new extension point or an `Env` method, ask ledger-autobind; the engine owner adds it.

A rule is a small type with `Name()` and one method of an extension point, registered in `init` with the family:

```go
type pointerOptional struct{}
func (pointerOptional) Name() string { return "O1" }
func (pointerOptional) Type(env Env, up string, t types.Type) (Verdict, bool) { ... }
func init() { RegisterType(Optionality, pointerOptional{}) }
```

Extension points: `RegisterPlacement` (extra directories per upstream package), `RegisterName` (does a Go name stand for an upstream name), `RegisterMember` (which Go field or method stands for an upstream property), `RegisterType` (upstream type text against a Go type), `RegisterSignature` (upstream call shape against a Go signature). Built-in rules run first; yours run when the built-in verdict is not yes; the first deciding rule wins. Use `env.Agree` to recurse into element types.

## Tests
Each rule needs table tests in `<family>_test.go` with at least one case the rule must refuse and one where it must stay silent (`ok=false`). Use `go/types` fixtures (`go/parser` + `types.Config.Check` on a source string). Detector-level behaviour goes in the main package; see `engine_test.go` for the pattern (`rules.Isolate`).

## Trust checks to run before you push (from the repo root)
```
go run ./test/parity/cmd/prodreach -out /tmp/reach-ab.json -tags pig_experimental ./cmd/pig ./cmd/pig-experimental   # once
git show 549079d4d:test/parity/interfaces/mapping-v1.0.4.json > /tmp/hand-mapping.json          # once
go run ./test/parity/interface-closure/autobind -reach /tmp/reach-ab.json -hand-ledger /tmp/hand-mapping.json -closed-check -out /tmp/ab-closed   # trust check 1: hand-closed rows
go test ./test/parity/interface-closure/autobind/...                                            # trust check 2: seeded gaps + your rule tests
go run ./test/parity/interface-closure/autobind -reach /tmp/reach-ab.json -out /tmp/ab-out      # totals
go tool golangci-lint run --build-tags=integration,live,parity ./test/parity/interface-closure/autobind/...
```
Report the before/after of: closed-check agreement, NOT-A-GAP and GAP counts, and the reasons you moved. Do not run `-update` and do not touch `mapping-v1.0.4.json` or the gap baseline; the engine owner regenerates them after merging.
Push to the lane branch and report the lane name, the commit and the family number to the engine owner.

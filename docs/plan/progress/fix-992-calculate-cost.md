# fix-992-calculate-cost

Scope: widen `ai.CalculateCost` from `*Model` to `AnyModel`, as upstream 0.99.2 `calculateCost(model: AnyModel, usage)` (`.upstream/v0.99.2/packages/ai/src/models.ts:1193-1213`; the same signature is in v0.99.1). Owner approved 2026-10-01. Interface row `pkg:ai/models#calculateCost` (+ `::call:0`) in `test/parity/interfaces/mapping-v0.99.2.json`.

## Red

Pi has no test that calls `calculateCost` with an image or classifier model (`grep calculateCost .upstream/v0.99.2/packages/ai/test` finds only `models-runtime.test.ts:152,155`, chat). The red tests reuse that test's inputs and expected values (`models-runtime.test.ts:126-160`) for all three model types. Stub: `CalculateCost(m AnyModel, ...)` prices only `*Model` (type assertion), so image and classifier prices come out zero.

`go test ./ai -run TestCalculateCost` on the stub: `TestCalculateCostAcceptsAnyModelType/image` and `/classifier` fail with `short={... Total:0}`; the nil-model test passes (the stub already treats nil as unpriced). (The chat sub-case first failed from a wrong expected value in my own test, 15.5 not 7: the 1M-token prompt is above the tier threshold. Fixed before the red commit.)

## Green

- `ai/model_types.go`: `AnyModel` gains `CostRates() ModelCost` (`types.ts` BaseModel.cost); `*ImageModel` and `*ClassifierModel` implement it (nil-safe like `*Model`).
- `ai/model_utils.go`: `CalculateCost(m AnyModel, usage *Usage)`; a nil interface or typed-nil model has no price. Every existing call site passes `*Model` and compiles unchanged.
- `ai/system_one.go` `parseUsage` now calls `CalculateCost(&model, &usage)` (`system-one-shared.ts:143`). Before, it copied only the four base rates, so classifier cost tiers were ignored. Added `TestSystemOneUsageAppliesClassifierCostTiers`; mutation-checked by restoring the old call (it fails with the base-rate cost 0.004 instead of 0.04).
- Mapping `pkg:ai/models#calculateCost` and `::call:0` in `mapping-v0.99.2.json`: partial -> ported (production complete; evidence: the three new tests plus the two existing ones). `call:ai/system_one.go#parseUsage` added as production caller.
- The stale `pkg:ai/.#calculateCost` rows (pending, root-barrel entries) are not part of this lane's row; left as is.

## Gates

`go build ./...`, `go vet` (ai, coding, internal/codingagent), `GOOS=windows go vet ./ai/`, gofmt on touched files, `go fix -diff ./ai/` empty, `make interface-go-drift interface-mapping-quality interface-recommendations-drift` clean after regeneration, `make parity-family FAMILY=ai-sdk` ok (3 scenarios ran), `go test -race` on ai, agent/..., coding, coding/extension, coding/extension/host/subprocess, internal/codingagent/... pass, and `GOMAXPROCS=4 -race -count=24` on the cost tests passes.

- Pre-existing and not mine: `ai/openai.go:1084` goimports finding (file untouched). `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` needs Xvfb, which this host lacks.
- Before `npm install` in `extensions/sdk-ts` (done by `make parity-family`), ~80 Node-dependent tests failed with a missing `node_modules`; they pass after.

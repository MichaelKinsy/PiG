# fix-992-ledgers

Scope: ledger and mapping corrections for upstream 0.99.2: the six ai rows of `test/parity/upstream-sync/v0.99.2.toml`, the 207 pi-ai interface IDs in `test/parity/interfaces/mapping-v0.99.2.json`, and stale `docs/parity/PORT_MAP.md` rows. Base: staging `porter/pi-0.99.1` at 94f45865e plus `fix-992-classify` (merged first, as the task says). Sources: audit-992-ai F7 (`docs/plan/progress/audit-992-ai.md` on `audit-992-ai`) and the fix-992-classify READY question. Upstream cites use `.upstream/v0.99.2/...` because every behavior here changed or was added in 0.99.2.

## Red/green

This lane changes ledgers only, so it has no production change and no upstream test to port: `models.ts` is byte-identical between 0.99.1 and 0.99.2 (`diff` empty), and the only new upstream test in scope, `packages/ai/test/models-entry.test.ts`, loads the Node `./models` entry under a module hook that forbids heavy dependencies, which has no Go form (see the QUESTION). The red state is the gates themselves on the base:

- `go run ./test/parity/cmd/upstreamdelta`: pending rows (`config.ts`, `worker.ts` stay pending for the owner; the six ai rows were already classified by fix-992-classify, see A).
- `make interface-mapping-quality` passes on the base but 207 pi-ai IDs were `pending` (196 `pkg:ai/models#*`, 5 `pkg:ai/compat#ANTHROPIC_*_ENV`, 2 `UnsupportedStrictSchemaKeywordCheck`, `makeStrictJsonSchema` and `resolveJsonSchemaStrictSampling` with their calls); `resolveJsonSchemaStrictSampling` had regressed from `deferred`.
- `make port-map-drift` was clean but the rows named below carried text that contradicts the tree.

## A. upstream-sync v0.99.2: the six ai rows

fix-992-classify had already written all six as `ported`. I re-probed each against the audit findings on this base (the audit's red tests copied into a scratch checkout and run, then removed):

| row | audit finding | still failing here? | disposition |
|---|---|---|---|
| `api/anthropic-messages.ts` | F1 ambient `ANTHROPIC_AUTH_TOKEN` beats federation (`TestAudit992DirectAnthropicStreamIgnoresAmbientAuthTokenForFederation`), F2 token endpoint failure text (`TestAudit992FederationUnreachableTokenEndpointMessage`) | yes, both | `deferred`, rationale names F1/F2, `fix-992-federation-auth` flips it |
| `api/constrained-sampling.ts` | F5 object keyword key order in the strict `require` reason (`TestAudit992StrictRequireReasonKeepsObjectKeyOrder`) | yes | `deferred`, rationale names F5 and its owner `fix-992-ai-js-semantics` (review correction: that lane exists on staging) |
| `utils/provider-retry.ts` | F4 Date.parse grammar | no: fixed by 79d5b3d55 (`jsDateParse`); `TestAudit992RetryAfterAcceptsDateParseFormats` passes | stays `ported`; evidence adds `TestProviderRetryDelayAcceptsEveryDateParseForm` and the rationale says the date branch reads through V8's Date.parse |
| `env-api-keys.ts` | none (five names) | n/a | stays `ported` |
| `providers/anthropic.ts` | F3 registry/CLI availability ignores federation-only env (`TestAudit992FederationEnvConfiguresAnthropic`), F6 Node bridge rejects it (`TestAudit992NodeBridgeForwardsFederationOnlyAnthropicRequest`) | yes, both | `deferred` (review correction, rev-fix-992-ledgers): in Pi this resolver delta alone makes `hasConfiguredAuth` true through `models.checkAuth` (model-runtime.ts:336-353), and no other sync row tracks F3/F6 |
| `utils/overflow.ts` | none | n/a | stays `ported` |

`upstreamdelta` has no `partial`; `deferred` needs a rationale and is accepted. `config.ts` and `worker.ts` remain `pending` (owner decision, fix-992-classify question). `make upstream-delta` still fails on exactly those two.

## B. Interface mapping (207 IDs)

Method: a type-resolved scan (`golang.org/x/tools/go/packages`, scratch program outside the repo) lists every non-test caller of every `ai.Models` and `ai.ModelsProvider` member; each upstream member is paired with its Go declaration by reading `models.ts` (0.99.2) and the Go source; evidence is an existing upstream-ported test that names the Go member (a script rejects a cited file that does not contain the member's name or a test function that does not exist); `make interface-mapping-quality` validates every target, production and evidence fragment as a Go declaration. Rows are not bulk-marked: each row carries its own Go target, production call site and test. A row is `ported` only with a production call site and all three layers complete; a member with no production caller is `partial` with the reason in its rationale. The parent rows (`Models`, `MutableModels`, `Provider`, `CreateProviderOptions`) are `partial` while any child is, because the gate rejects a ported parent with a partial child.

Result: **150 ported, 57 partial, 0 pending** among the 207.

Partial rows and why:

- `Models`/`MutableModels` members `classify`, `complete`, `completeSimple`, `generateImages`, `getAllModels`, `getAvailableOfType`, `getModelOfType`, `getModelsOfType`, `clearProviders` (and their `::call` rows): the type scan finds no production call on an `*ai.Models`. Production reaches these operations through `coding.ModelRuntime` (its own implementations, `coding/model_runtime_*.go`) or the typed provider. `clearProviders` is called by Pi's `ModelRuntime.reload` (`model-runtime.ts:319`) while PiG unregisters one provider at a time.
- `Provider.baseUrl`/`headers`: only Pi's bug report reads them (`bug-report.ts:118-119`); PiG's bug report is built from registry data.
- `CreateProviderOptions` `baseUrl`, `headers`, `fetchModels`, `filterModels`, `filterAllModels`: no production caller sets them; the registry composes `ModelsProvider` by hand.
- `calculateCost`: takes `AnyModel` upstream, `*Model` in Go (image and classifier models price through the unexported `calculateUsageCost`).
- `hasApi`: no production caller in Pi or PiG; behavior tested.
- `ModelsError` (`name`, `stack`, parent): no Go counterpart for inherited `Error.name`/`stack`.
- `ModelsImagesOptions`: consumed in production only by the `coding.ModelRuntime` re-implementation.
- `makeStrictJsonSchema`, `resolveJsonSchemaStrictSampling` (+ calls): behavior partial, F5.

`resolveJsonSchemaStrictSampling` is no longer `pending`: it is `partial` with the F5 reason (it had been `deferred` in 0.99.1, a carried scope decision that a behavior change must re-open).

A premise I got wrong and corrected while verifying: I first wrote that Pi's coding agent never passes `providers` to `refresh`. It does (`model-runtime.ts:602`), and PiG's `synchronizeCredentialState` does too (`internal/codingagent/native_credential_operations.go:94`), so `ModelsRefreshOptions.providers` is `ported`.

## C. PORT_MAP

- `anthropic-messages.ts` (🟡): adds `ai/anthropic_federation.go`, its tests, the strict-keyword fallback, and states the open F1/F2 gaps.
- `providers/anthropic.ts` (✅ → 🟡): states exactly what exists (the ai resolver reads the five federation variables and exchanges the token) and what does not (registry and CLI availability ignore federation-only env, F3; the Node builtin-API bridge rejects it, F6). The old text claimed env auth on both SDK and CLI model paths.
- `pi-user-agent.ts`, `management-http.ts`: `remote-catalog-provider.ts` is ported (`internal/codingagent/remote_catalog_provider.go`, which sends the User-Agent and uses `managementhttp.FetchWithRetry`).
- `virtual-models.ts` (🟡): lists the router and catalog overlay in `coding/virtual_models.go` and both ported upstream tests; status stays 🟡 until a reviewer closes behavior.
- `remote-catalog-provider.ts` row was already correct.

## Gates

See the READY commit message. Pre-existing failures on the base that this lane does not touch: `correspondence-check` and `closure-check` (two closed `known-gaps.toml` correspondence findings: `fullscreen-wheel-scroll-lines`, `settings-selector`) and `known-gaps-drift` (`images-models.test.ts` designedOutCases differ from the approved policy list).

## Generated files

Regenerated in the last commit (`chore(port-99): regenerate generated files`): coverage block and report (the porting count moves 421 → 420 because `providers/anthropic.ts` is no longer ✅).

## Stubs other families must fill

None. Rows to flip when other lanes land: `anthropic-messages.ts` sync row and the F1/F2-dependent mapping notes (`fix-992-federation-auth`); `constrained-sampling.ts` sync row and the two `resolveJsonSchemaStrictSampling`/`makeStrictJsonSchema` mapping rows (F5 owner).

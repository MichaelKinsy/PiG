# Post-login model selection and input privacy evidence

This report records the public-main PR #74 qualification, not the current candidate. The [candidate integration report](post-login-model.md) records its shared-lock, model-runtime and later ownership refinements.

## Reference contract

The reference is Pi 0.87.1 (`f07218c4d4bbc12bef056a7058c3dd49dfe41abe`). Paths below are relative to its source root.

| Behavior | Pi source | PiG implementation |
|---|---|---|
| Provider defaults and declaration order | `packages/coding-agent/src/core/model-resolver.ts:20-64` | `internal/codingagent/default_models.go`; startup and login share one table |
| Unknown-model condition | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:298-300` | `isUnknownModel`; nil represents PiG's initial unknown sentinel |
| Selection, discovery, guidance and messages | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:5879-5975` | `completeProviderAuthentication`, `finishProviderAuthentication`, `postLoginModel` |
| Model/default/thinking mutation before awaited notifications | `packages/coding-agent/src/core/agent-session.ts:2110-2137` | `Session.SetModelOnMain` separates owner-loop state mutation from worker-side notifications |
| Submitted text and prompt layout | `packages/coding-agent/src/modes/interactive/components/login-dialog.ts:56-64,77-81,156-182` | `tui.LoginDialog`, with the selectable privacy feature disabled |
| Secret-prompt classification | `packages/ai/src/auth/helpers.ts:12-16`; `interactive-mode.ts:6085-6093` | Standard API-key prompts use the provider's auth-method name and secret classification |

Radius's catalog-order fallback and llama.cpp guidance remain Pi's own special cases. No login path hard-codes a Copilot or Anthropic model. `TestDefaultModelPerProviderMatchesPinnedUpstream` independently parses the pinned table. Tests cover OAuth, ordinary API keys, custom-base-URL OpenAI-compatible providers and providers without a default.

## Model completion and credential paths

Model construction runs off the input loop. `Session.SetModelOnMain` dispatches synchronous model, transcript, default and thinking-state changes to the owner loop before awaiting extension notifications on the worker. It uses public main's existing `BeginModelChange` state reducer. The ordinary and RPC paths preserve their existing thinking-notification ordering. This does not import the candidate's Model Runtime, provider or Session changes.

Generation and Session/handle/model checks reject stale work before mutation and before UI/scope changes. Model and Session commands invalidate the generation even when the model pointer stays the same. A late notification callback never re-applies or re-persists an old choice. Refreshes remain bounded to 15 seconds and owned by the interactive lifetime. No selection scans Session history.

External credential stores belong to PiG's existing capability D40. Pi always reports `getAuthPath()` at `interactive-mode.ts:5933,5937,5947`; it does not return a provider-owned store path. PiG preserves the actual `StoreOAuthCredentials` result through immediate and deferred completion, and uses `auth.Path()` for core storage.

Regression guards:

- `TestAPIKeyLoginSelectsProviderDefault` and `TestAPIKeyLoginWithoutDefaultReportsGuidance`: shared completion after API-key authentication.
- `TestAPIKeyLoginCustomEndpointDoesNotInventDefault`: preserve the configured endpoint and report missing-default guidance rather than choosing the first custom model.
- `TestPostLoginModelDiscovery`: Pi's `test/suite/regressions/7027-credential-refresh-hang.test.ts:128-219`, including preferred default, catalog order, empty catalog, errors, timeout, replacement and shutdown. It also checks an external credential path across deferred discovery.
- `TestRegisteredOAuthLoginReportsReturnedCredentialPath`: report an external store with and without a selected default.
- `TestPostLoginSelectionLosesToOwnerLoopCommands` and `TestPostLoginSelectionRechecksSessionMutationAndCompletion`: reject delayed construction or completion after a newer choice.
- `TestSetModelOnMainDispatchesAllStateBeforeNotifications`: the real Session boundary, including rejected mutation and notifications completing after a newer model is selected. A compiling mutation that moves state mutation before dispatch fails both cases with `state changed before owner dispatch`.

The four model/status scenarios under `test/parity/scenarios/oauth/10-*` through `13-*` compare real runtime state, persisted defaults and completion messages against Pi. Status comparisons normalize only isolated credential-file paths. The observer never selects a model.

## Configurable input privacy

Owner-approved divergence D80 defines `maskSecretInput`. It defaults to true and appears as **Mask secret input** in `/settings`. The description and prompt hint explain the difference from Pi and the false opt-out.

When enabled, the dialog shows up to eight dots, a grapheme count and the last four graphemes. Inputs shorter than five graphemes show no suffix. Submission retains the preview, not the full input. Authentication progress and error text redact submitted masked values and their trimmed forms before display. Login input is not appended to Session messages. The authentication flow still receives the submitted value, and the authorized credential store still stores credentials: this is display privacy, not credential encryption.

When disabled, PiG displays the full text while editing and after submission. Pi 0.87.1 does this even for `type: "secret"`. No comparison substitutes dots for Pi's plaintext output. The exact comparison exposed dialog spacing, prompt/placeholder/hint styling, API-key prompt-label and dynamic-border reset differences; these are fixed at their owning components.

Guards:

- `TestLoginDialogMaskedPreview`: empty, short, ordinary, long and Unicode inputs, with suffixes, counts, hint, retained text and redacted progress.
- `TestLoginDialogSecretValueNeverRendered`: inspect both rendered frames and `LoginDialog.lines`, so render-time redaction cannot hide retained plaintext.
- `TestLoginDialogMaskDisabledMatchesPi`: compare complete ANSI frames from the installed Pi dialog before typing, during typing, after submission and after progress, without ANSI or whitespace normalization.
- `TestMaskSecretInputSettingsRoundTrip`, `TestMaskSecretInputSettingsMenuAppliesToNextDialog` and `TestLoginMaskSettingReachesStandardDialog`: default, explicit values, persistence, menu and production-dialog wiring.
- `TestAPIKeyLoginPromptMasksInput`, `TestLlamaLoginNeverRendersSubmittedSecret` and `TestMaskedLoginErrorDoesNotEnterFramesOrSession`: standard and typed prompt boundaries, diagnostics and persisted Session privacy.
- `TestPiIgnoresMaskSecretInputInSharedSettings`: Pi reads the Go-serialized boolean, ignores it for its behavior and preserves it when changing another setting; PiG then reads the result. This tests JSON content, not interoperability of the two settings-lock protocols. No candidate lock implementation is included.
- `oauth/14-login-secret-mask-disabled`: escaped-output equality against real Pi, with three runs.

The settings correspondence rules account for exactly one D80 row and still reject undeclared additions or reordered Pi rows. The settings scenario asserts each raw row count before accounting for this approved difference. The process and tmux launchers discard ambient agent-directory overrides before applying their snapshotted configuration; unit tests guard both boundaries.

## Verification environment and scope

The clean public branch starts at `10111db80`: original #74 plus public main `c1550a84a`. It contains no candidate merge. Only login/privacy code, its tests, documentation and feature-derived inventories are added. Release notes are in `CHANGELOG.md` under `[0.3.0]`.

Use Go 1.27.1 and real Pi 0.87.1. Resolve tool-manager shims to installed Go, Node, Rust and uv executables before changing HOME. Every test and probe isolates HOME, PIG_HOME and both agent directories. Compiler/package caches are separate from credentials. The `make ci-parity` comparison runs that target on this branch and an unmodified archive of public main `c1550a84a`, with the same installed oracle and toolchains.

The earlier review identified a full-suite claim that contradicted a reported `TestParseChangelog_RealFile` failure. That historical claim is not acceptance evidence. The clean branch passes `TestParseChangelog_RealFile`; no full `go test ./...` pass is inferred from focused testing.

## Verification results

Build, Linux vet, Windows application vet, full lint with integration/live/parity tags, changed-package lint, `go fix -diff ./...` and focused/race tests pass. Complete `coding`, `internal/codingagent` and `tui` package tests pass. `make ci-contracts` and `make ci-drift` pass. The coverage and Go-interface inventories are regenerated from this branch, not copied from the candidate; committing those feature-derived updates fixes the stale-inventory failures.

| Check | Public main `c1550a84a` | Clean PR branch |
|---|---|---|
| `make ci-parity` | Pass, 274 scenarios run | Pass, 279 scenarios run |
| Branch-only failures | None | None |
| OAuth, settings, model-resolver-selector and selectors declared durability | Not separately repeated | Pass |

The five additional scenarios are the original PR's four post-login cases and the masking-off case. All tracked blobs in the public-main archive were checked against the Git tree after the run; none changed. There are no inherited failures to allow and no comparator weakenings, retries or longer timeouts. The existing disabled-masking scenario also rejects a compiling mutation that ignores the setting, reporting a missing plaintext key and unequal escaped output.

The local evidence includes the paired-run result files, gate logs, rejected owner-dispatch and retained-secret mutations, and the installed-Pi ANSI comparison. No `go test ./...` pass is claimed.

## CI deadline regression

The CI timeout case exposed a test ordering assumption, not a production deadline change. A `time.Sleep(15 * time.Second)` in the synctest bubble woke at the same instant as the refresh's context timer. The sleeping test could run first and release the blocked store before the context's cancellation callback ran. `RefreshCatalogs` could then correctly return `Aborted: false`; the worker's deferred cleanup cancellation was not evidence that the deadline had fired. The unfixed timeout case failed 31 of 200 race-detector runs locally.

Both deadline tests now wait on the captured refresh context's `Done` channel before releasing the store. They assert `context.DeadlineExceeded` and exactly 15 seconds of virtual elapsed time. This mirrors Pi's awaited `advanceTimersByTimeAsync(15_000)` in `packages/coding-agent/test/suite/regressions/7027-credential-refresh-hang.test.ts:121,214`, which delivers the abort callback before the test checks the warning. No production behavior, sleeps, retries or timeout budgets are added. A compiling mutation that changes the production deadline to 16 seconds fails both tests at the exact elapsed-time assertion.

The active-divergence header is corrected to 32 using the existing `## D<N>` sections as the denominator. The bundled reference already lists D80 and has no numeric count header.

`go test -race ./internal/codingagent -run '^(TestPostLoginModelDiscovery|TestPostLoginCompletesBeforeBackgroundRefresh)$' -count=200` passes normally and under CPU stress: two CPU cores, `GOMAXPROCS=2` and four ready SHA-256 load workers sharing those cores. Both runs use the existing test timeout. `CI=1 make ci-test-fast` passes across all 113 packages selected by the unmodified fast-shard dispatcher. The first local shard run found an old public-main comparison snapshot inside the checkout; moving that disposable snapshot outside the source tree fixes the source-scan failure without changing any test or exclusion. Linux/Windows vet, full lint, `ci-contracts` and `ci-drift` also pass.

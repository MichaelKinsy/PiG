# Post-login model selection and input privacy evidence

## Reference contract

The reference is Pi 0.87.1 (`f07218c4d4bbc12bef056a7058c3dd49dfe41abe`). Paths below are relative to its source root.

| Behavior | Pi source | PiG implementation |
|---|---|---|
| Provider defaults and declaration order | `packages/coding-agent/src/core/model-resolver.ts:20` | `internal/codingagent/default_models.go`; startup and login share one table |
| Unknown-model condition | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:298-300` | `isUnknownModel`; nil represents PiG's initial unknown sentinel |
| Selection, discovery, guidance and messages | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:5879-5975` | `completeProviderAuthentication`, `finishProviderAuthentication`, `postLoginModel` |
| Model/default/thinking mutation before awaited notifications | `packages/coding-agent/src/core/agent-session.ts:2110-2137` | `Session.SetModelOnMain` separates owner-loop state mutation from worker-side notifications |
| Submitted text and prompt layout | `packages/coding-agent/src/modes/interactive/components/login-dialog.ts:56-64,77-81,156-182` | `tui.LoginDialogComponent`, with the selectable privacy feature disabled |
| Secret-prompt classification | `packages/ai/src/auth/helpers.ts:12-16`; `interactive-mode.ts:6085-6093` | Standard API-key prompts use the provider's auth-method name and secret classification |

Radius's catalog-order fallback and llama.cpp guidance remain Pi's own special cases. No login path hard-codes a Copilot or Anthropic model. `TestDefaultModelPerProviderMatchesPinnedUpstream` independently parses the pinned table.

## Model completion and credential paths

Model construction runs off the input loop. `Session.SetModelOnMain` dispatches synchronous model, transcript, default and thinking-state changes to the owner loop before awaiting extension notifications on the worker. Generation and Session/handle/model checks reject stale work before mutation and before UI/scope changes. Model and Session commands invalidate the generation even when the model pointer stays the same. A late notification callback never re-applies or re-persists an old choice. Refreshes remain bounded to 15 seconds and owned by the interactive lifetime. No selection scans Session history.

External credential stores belong to PiG's additive capability D40. Pi itself always reports `getAuthPath()` at `interactive-mode.ts:5933,5937,5947`; it does not return a provider-owned store path. PiG preserves the actual `StoreOAuthCredentials` result through immediate and deferred completion.

The original regressions remain in place:

- `TestAPIKeyLoginSelectsProviderDefault` and `TestAPIKeyLoginWithoutDefaultReportsGuidance` failed before shared completion was implemented.
- `TestPostLoginModelDiscovery` ports Pi's `test/suite/regressions/7027-credential-refresh-hang.test.ts:128-219`, including preferred default, catalog order, empty catalog, errors, timeout, replacement and shutdown. It also checks an external credential path across deferred discovery.
- `TestRegisteredOAuthLoginReportsReturnedCredentialPath` failed when completion discarded the returned location.
- `TestPostLoginSelectionLosesToOwnerLoopCommands` and `TestPostLoginSelectionRechecksSessionMutationAndCompletion` failed when delayed construction or completion overwrote newer choices.
- `TestSetModelOnMainDispatchesAllStateBeforeNotifications` verifies the real Session boundary, including rejected mutation and notifications completing after a newer model is selected.

The four model/status scenarios under `test/parity/scenarios/oauth/10-*` through `13-*` compare real runtime state, persisted defaults and completion messages against Pi. They normalize only isolated credential-file paths where necessary. Compiling mutations disabling selection and replacement guards made these guards fail.

## Owner decision: configurable input privacy

The owner decision dated 2026-09-27 replaces unconditional masking with divergence D80, `maskSecretInput`. It defaults to true and appears as **Mask secret input** in `/settings`. Its description and prompt hint explain the difference from Pi and the false opt-out. The integration ledger already allocated the earlier branch-local identifier to another capability. `docs/parity/DIVERGENCE-IDS.txt` now assigns privacy D80 without changing the existing allocations.

When enabled, the dialog shows up to eight dots, a grapheme count and the last four graphemes. Inputs shorter than five graphemes show no suffix. Submission retains the preview, not the full input. Authentication progress and error text redact submitted masked values and their trimmed forms before display. Login input is not appended to Session messages. The authentication flow still receives the submitted value, and the authorized credential store still stores credentials: this is display privacy, not credential encryption.

When disabled, PiG displays the full text while editing and after submission. Pi 0.87.1 does this even for `type: "secret"`: an installed-Pi probe through `showAuthPrompt` confirmed plaintext both before and after submission. No comparison substitutes dots for Pi's plaintext output.

The disabled comparison exposed pre-existing dialog drift that is fixed at its source: extra blank rows, unstyled prompt/placeholder/hint text, the generic API-key prompt label, and the default dynamic border's full reset instead of Pi's foreground reset. The prompt label now comes from `BuiltinProviderAuth(...).APIKey.Name`, including distinct OpenAI and Gemini labels.

New guards:

- `TestLoginDialogMaskedPreview`: empty, short, ordinary, long and Unicode inputs; suffixes, counts, hint, retained text and redacted progress. It failed before the configurable preview was implemented.
- `TestLoginDialogSecretValueNeverRendered`: no full masked value during editing, submission, progress or a later prompt.
- `TestLoginDialogMaskDisabledMatchesPi`: compares complete ANSI frames from the actual installed Pi dialog before typing, during typing, after submission and after progress. No ANSI or whitespace normalization is used.
- `TestMaskSecretInputSettingsRoundTrip`: default true, explicit false/true, serialization and reload.
- `TestMaskSecretInputSettingsMenuAppliesToNextDialog` and `TestLoginMaskSettingReachesStandardDialog`: `/settings` and persisted settings control the production dialog.
- `TestAPIKeyLoginPromptMasksInput` and `TestLlamaLoginNeverRendersSubmittedSecret`: secret handling at standard OpenAI/Google and typed llama.cpp caller boundaries.
- `TestMaskedLoginErrorDoesNotEnterFramesOrSession`: a builder that echoes the key cannot put it into rendered error text, Session entries or files outside credential storage.
- `TestPiIgnoresMaskSecretInputInSharedSettings`: Pi reads the Go-serialized boolean without errors, ignores it for its own behavior, preserves it while changing another setting, and PiG reads the resulting file.
- `oauth/14-login-secret-mask-disabled`: compares the actual unmasked API-key login dialog against real Pi with `escaped_output_equal` and three runs. The same extra key is present in Pi's settings fixture.

## Shared-settings boundary found during probing

The earlier isolated-branch probe reported `ELOCKED` because Go used a persistent flock sidecar named `settings.json.lock`, while Pi used a directory with that name. The integration merge supplies `internal/pilock` for settings/auth/model stores and the `PIG_USE_PI_DIRS` implementation. Shared-directory tests are re-run on that implementation; the earlier lock failure is historical evidence, not a claim that the merged code retains it.

The shared-key guard now uses the merged settings writer, Pi's settings writer, and a Go reload in sequence. The integration's shared-backend and Pi-directory tests own the broader interoperability surface. No stale sidecar is removed by this change.

## Verification environment

Use Go 1.27.1 and the pinned real Pi package. Resolve tool-manager shims to installed Go, Node, Rust and uv binaries before changing HOME. Every run isolates HOME, PIG_HOME, PIG_CODING_AGENT_DIR and PI_CODING_AGENT_DIR in temporary directories. Compiler/package caches remain separate from credential directories.

## Verification results

The owner-decision revision before the merge train passed `go test ./...` on Go 1.27.1 with the isolated environment above. That is historical evidence, not a full-suite claim for the merged train. The integration's batch owner runs the full suite. The focused post-login/privacy/model-owner race regressions and imported integration cases pass on merged train `8a880b6e0`; this includes the real Go-settings-write → Pi-settings-write → Go-reload test using the shared directory-lock backend. Linux vet, Windows application vet, and touched-package lint with parity tags pass. OAuth and model-resolver-selector parity families pass. Current settings parity also accounts for the single D80 row in the train's tmux-image-capability scenario.

Local generators were run for contract/drift validation, then generated outputs were restored to the integration-owned versions before committing. `ci-contracts` reaches the train's `test-porting-release` gate and fails on existing pending/partial hot-path test dispositions. `ci-drift` reaches the existing D78 record, which lacks `SCRUTINIZED:approved`. Those records were not silently approved or weakened. The remaining divergence guard, source-hygiene and docs-drift gates pass.

The added settings row has explicit D80 lineage in the correspondence rules. Tests still reject unclaimed additions, missing declared additions and reordered Pi rows. The settings scenario asserts Pi's raw 31-row count and PiG's raw 32-row count before accounting only for this one approved additive difference. Its other bytes and ANSI remain compared, and durability increased from one run to three.

Cross-family verification under isolated parent directories exposed a parity-harness issue: ambient agent-directory variables overrode declared fixture homes, including through the tmux server's inherited environment. `TestHermeticEnvironmentDoesNotInheritAgentDirectories` and `TestTmuxPrefixDoesNotInheritAgentDirectories` failed before the fix. Process/RPC and tmux launchers now discard those ambient overrides before applying their snapshotted configuration. Selector scenarios passed afterward without changing their resource assertions.

The initial probes were not green: new preview/setting tests failed, disabled-mode comparison exposed prompt/layout/reset drift, the shared-file probe exposed the former lock incompatibility, and gates rejected the previously unaccounted additive row. The merge retains both sets of post-login tests, the train's auth validation and state reducer, the explicit unknown sentinel, and owner-loop theme/render servicing during secret entry. The deadline regression now waits for cancellation delivery at the exact virtual deadline before releasing its blocked catalog, rather than racing those two events.

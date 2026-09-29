# W3 model/footer acceptance evidence

## Finding 5: interactive startup thinking

Pi 0.87.1 selects and clamps the Session level in `packages/coding-agent/src/core/sdk.ts:231-255`. Its footer reads `state.thinkingLevel` in `packages/coding-agent/src/modes/interactive/components/footer.ts:186-190`. Interactive mode does not select that level again.

PiG's `initThinkingLevel` independently looked up the requested level in the supported-level slice. A missing `medium` entry selected index zero (`off`) instead of the Session's clamped `high`. It wrote this value into the live agent without updating the saved Session. It also overwrote restored Session levels with current settings.

The fix removes that second selection and its redundant CLI option. Startup uses the existing state-binding method shared with model changes. No provider-specific branch is added.

Red evidence on candidate `e04490990`:

- `TestInteractiveStartupPreservesSessionThinking` fails for DeepSeek, Anthropic, OpenAI, and GitHub Copilot model metadata. The state changes from the supplied Session level to a settings-derived level.
- `footer/09-footer-session-thinking` compares both complete escaped footer rows. Pi shows `deepseek-flash • high`; PiG shows `deepseek-flash • thinking off`.
- `interactive-thinking-wire.py pig` captures `{"thinking":{"type":"disabled"}}` and no `reasoning_effort` in the first interactive DeepSeek request. Pi sends enabled thinking and `reasoning_effort: "high"`. This confirms that finding 5 affects requests, not only the label.

Green evidence:

- The regression unit test passes.
- `footer/09-footer-session-thinking` and `footer/10-interactive-thinking-wire` pass three paired runs against real Pi 0.87.1.
- The wire fixture drives fresh and resumed interactive Sessions, awaits actual assistant replies, and checks the persisted thinking entries. It covers DeepSeek plus custom-base-URL, no-default-model providers using both `openai-completions` and `openai-responses`. Resume changes the global default to `off` before opening the saved `high` Session. No real credentials or external network are used.
- The terminal scenario types an unsubmitted editor marker before capture. Pi can emit the first footer before redrawing its startup header; the marker supplies a post-startup input/render acknowledgement instead of a timing sleep.
- Before the merge train, the existing hermetic footer and model-runtime-store-catalog families pass their declared three pairs. After merging integration `71e076222`, all eight hermetic footer scenarios pass again. The two existing `requires-auth` footer scenarios are not claimed as verified.
- The binding benchmark records 0 B/op and 0 allocs/op. The binding reads no history, starts no worker, and performs no I/O. CPU and allocation profiles are retained with the lane logs. No performance improvement is claimed.

The user documentation changes in `docs/site/docs/models.md` describe startup precedence, clamping, and the common Session source of truth. `changelog.d/w3-models-footer.md` records the fix.

### Gate results after the merge train

Integration `71e076222` merges cleanly. The focused startup/model-change tests pass under `-race`, touched-package lint reports zero issues, and touched-package vet passes on Linux and with `GOOS=windows`. The pre-merge full `cmd/pig` and `internal/codingagent` package tests pass; the post-merge full codingagent run fails in the integration-owned `TestInteractiveUserBashEmptyResultPort`. Post-merge root `go vet ./...` fails because eight integration-owned test sites still use the removed `coding.Session.model` field. None of those files are changed by this lane.

The required contract/drift gates are attempted before and after merging. After local regeneration, `ci-contracts` reaches inherited recommendations-inventory drift; `ci-drift` passes scenario lint, file mapping, coverage drift, and divergence consistency, then rejects pending D78 scrutiny. Before the merge, `ci-contracts` reaches the existing pending hot-path test obligations. No baseline, exception, timeout, or assertion is relaxed to pass these gates. Generated files are restored for integrator ownership.

Lane command output, red/green terminal artifacts, captured failure payload, and profiles are retained in the lane evidence directory. These gate failures and finding 3 prevent a COMPLETE report for this slice.

## Finding 3: catalog refresh remains blocked

This branch has no implementation of `packages/coding-agent/src/core/remote-catalog-provider.ts`; `docs/parity/PORT_MAP.md` records it as not implemented. Pi wraps built-in providers at `core/model-runtime.ts:183-190`, then restores, revalidates, persists, and publishes the overlay at `core/remote-catalog-provider.ts:57-135`. The current coordinator can report success after refreshing no built-in remote catalogs. This is a real difference, not a successful refresh or a provider-specific catalog count issue.

D64 and the recorded remote-catalog owner decision require `https://pi-in-go.dev/api/models/providers/<encoded provider ID>`, not `pi.dev`, with D65 identity. No credential-bearing fallback to Pi's host is permitted. The earlier catalog work is coupled to the pending raw Provider/model carrier representation (notably `9e2aa8810` and `3b8f6836e`). Its handoff identifies private-platform route deployment as a lead/platform blocker. The earlier typed implementation loses accepted raw catalog fields and non-string IDs; restoring it would reintroduce known drift.

Finding 3 needs the owner to coordinate that client integration and the private-platform deployment. This lane does not duplicate the held representation work, contact the private service, change the success message to hide the missing behavior, or claim that the ten live OpenRouter models are fixed. The exact blocker is also recorded in the lane handoff.

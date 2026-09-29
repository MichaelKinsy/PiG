# Upstream test porting batches

## Dispatch contract

Baseline: `67621208de1e3fa3b4a9940082a7ae404794451d`, Pi 0.87.1. This document partitions every baseline pending and partial test file exactly once: **208 pending + 209 partial = 417 files**, across **93 batches**. Counts come from `upstream-tests-v0.87.1.json`; they are case sites, not expanded `it.each` matrices or a count of cases still missing in partial files.

Hot-path batches come first. Target Go packages combine existing evidence locations with the owning production area; they are navigation targets, not proof of closure. New harness execution/runtime packages need their separate API and production callers, not substitutions from the older agent loop. Read the mapping rationale to enumerate missing cases before changing a partial entry.

Assign one owner per upstream test file. Do not dispatch the validation batch to this slice: `fix-tool-arg-coercion` owns it. The six tool-regression files already closed here remain in their original batches with current disposition `ported`, so no baseline obligation disappears. Dispatch only rows still pending or partial. Test-file assignments do not authorize simultaneous edits to shared Go packages, SDK protocols, or generated ledgers; the lead coordinates those collisions and serializes integration and shared inventory updates.

Current mapping SHA-256: `45e862958b6cc93318e555f4d558474344ee3a02ca654c558d442476efa390e8`. Current dispositions below are a generated snapshot; the JSON mapping remains authoritative.

Regenerate after integration:

```bash
python3 automation/gen/pending-test-batches.py --baseline-commit 67621208de1e3fa3b4a9940082a7ae404794451d --version 0.87.1
```

## Batch index

| Batch | Priority | Area | Files | Upstream case sites | Still pending/partial |
|---|---|---|---:|---:|---:|
| PT-001-tools-agent-loop | hot-path | tools | 3 | 77 | 0 |
| PT-002-tools-builtin-tools | hot-path | tools | 8 | 35 | 0 |
| PT-003-tools-builtin-tools | hot-path | tools | 8 | 25 | 0 |
| PT-004-tools-builtin-tools | hot-path | tools | 5 | 90 | 0 |
| PT-005-tools-harness | hot-path | tools | 2 | 34 | 0 |
| PT-006-tools-validation | hot-path | tools | 1 | 9 | 0 |
| PT-007-providers-anthropic | hot-path | providers | 8 | 33 | 0 |
| PT-008-providers-anthropic | hot-path | providers | 7 | 33 | 0 |
| PT-009-providers-azure | hot-path | providers | 2 | 18 | 0 |
| PT-010-providers-bedrock | hot-path | providers | 8 | 50 | 0 |
| PT-011-providers-bedrock | hot-path | providers | 2 | 14 | 0 |
| PT-012-providers-catalog-options | hot-path | providers | 8 | 55 | 0 |
| PT-013-providers-catalog-options | hot-path | providers | 6 | 50 | 0 |
| PT-014-providers-catalog-options | hot-path | providers | 1 | 87 | 0 |
| PT-015-providers-catalog-options | hot-path | providers | 8 | 91 | 0 |
| PT-016-providers-catalog-options | hot-path | providers | 3 | 6 | 0 |
| PT-017-providers-cloudflare | hot-path | providers | 2 | 4 | 0 |
| PT-018-providers-credentials | hot-path | providers | 8 | 102 | 0 |
| PT-019-providers-credentials | hot-path | providers | 5 | 23 | 0 |
| PT-020-providers-google | hot-path | providers | 8 | 50 | 0 |
| PT-021-providers-google | hot-path | providers | 1 | 9 | 0 |
| PT-022-providers-mistral | hot-path | providers | 3 | 21 | 0 |
| PT-023-providers-openai-codex | hot-path | providers | 3 | 39 | 0 |
| PT-024-providers-openai-completions | hot-path | providers | 8 | 97 | 0 |
| PT-025-providers-openai-completions | hot-path | providers | 2 | 5 | 0 |
| PT-026-providers-openai-responses | hot-path | providers | 7 | 42 | 0 |
| PT-027-providers-shared-streaming | hot-path | providers | 6 | 104 | 0 |
| PT-028-providers-shared-streaming | hot-path | providers | 1 | 120 | 0 |
| PT-029-providers-shared-streaming | hot-path | providers | 8 | 107 | 0 |
| PT-030-providers-shared-streaming | hot-path | providers | 8 | 68 | 0 |
| PT-031-providers-shared-streaming | hot-path | providers | 3 | 36 | 0 |
| PT-032-providers-shared-streaming | hot-path | providers | 1 | 233 | 0 |
| PT-033-providers-shared-streaming | hot-path | providers | 4 | 92 | 0 |
| PT-034-providers-shared-streaming | hot-path | providers | 1 | 35 | 0 |
| PT-035-providers-shared-streaming | hot-path | providers | 5 | 110 | 0 |
| PT-036-providers-shared-streaming | hot-path | providers | 8 | 45 | 0 |
| PT-037-providers-shared-streaming | hot-path | providers | 3 | 3 | 0 |
| PT-038-sessions-compaction | hot-path | sessions | 8 | 61 | 0 |
| PT-039-sessions-compaction | hot-path | sessions | 8 | 52 | 0 |
| PT-040-sessions-compaction | hot-path | sessions | 3 | 8 | 0 |
| PT-041-sessions-harness | hot-path | sessions | 2 | 25 | 0 |
| PT-042-sessions-persistence | hot-path | sessions | 8 | 83 | 0 |
| PT-043-sessions-persistence | hot-path | sessions | 3 | 34 | 0 |
| PT-044-sessions-session-runtime | hot-path | sessions | 8 | 46 | 1 |
| PT-045-sessions-session-runtime | hot-path | sessions | 8 | 96 | 0 |
| PT-046-sessions-session-runtime | hot-path | sessions | 8 | 38 | 2 |
| PT-047-sessions-session-runtime | hot-path | sessions | 4 | 22 | 0 |
| PT-048-extensions-callbacks | hot-path | extensions | 6 | 70 | 0 |
| PT-049-extensions-harness | hot-path | extensions | 3 | 13 | 0 |
| PT-050-extensions-loading-resources | hot-path | extensions | 1 | 30 | 0 |
| PT-051-extensions-loading-resources | hot-path | extensions | 1 | 93 | 0 |
| PT-052-extensions-loading-resources | hot-path | extensions | 6 | 80 | 1 |
| PT-053-tui-input | hot-path | tui | 3 | 42 | 0 |
| PT-054-tui-input | hot-path | tui | 1 | 192 | 0 |
| PT-055-tui-input | hot-path | tui | 3 | 103 | 0 |
| PT-056-tui-input | hot-path | tui | 3 | 80 | 0 |
| PT-057-tui-rendering | hot-path | tui | 8 | 40 | 0 |
| PT-058-tui-rendering | hot-path | tui | 8 | 65 | 0 |
| PT-059-tui-rendering | hot-path | tui | 8 | 29 | 0 |
| PT-060-tui-rendering | hot-path | tui | 8 | 71 | 0 |
| PT-061-tui-rendering | hot-path | tui | 4 | 52 | 0 |
| PT-062-tui-rendering | hot-path | tui | 1 | 81 | 0 |
| PT-063-tui-rendering | hot-path | tui | 8 | 87 | 0 |
| PT-064-tui-rendering | hot-path | tui | 6 | 75 | 0 |
| PT-065-tui-terminal | hot-path | tui | 8 | 59 | 0 |
| PT-066-tui-terminal | hot-path | tui | 8 | 109 | 0 |
| PT-067-tui-terminal | hot-path | tui | 1 | 21 | 0 |
| PT-068-cli-packages | hot-path | cli | 2 | 13 | 0 |
| PT-069-cli-packages | hot-path | cli | 1 | 123 | 0 |
| PT-070-cli-packages | hot-path | cli | 1 | 1 | 0 |
| PT-071-cli-rpc | hot-path | cli | 4 | 27 | 0 |
| PT-072-cli-settings | hot-path | cli | 5 | 70 | 0 |
| PT-073-cli-startup-modes | hot-path | cli | 7 | 111 | 0 |
| PT-074-cli-startup-modes | hot-path | cli | 8 | 27 | 0 |
| PT-075-cli-startup-modes | hot-path | cli | 7 | 17 | 0 |
| PT-076-tools-harness-execution | reviewed non-hot | tools | 2 | 32 | 0 |
| PT-077-tools-harness-runtime | reviewed non-hot | tools | 1 | 9 | 0 |
| PT-078-providers-catalog-options | reviewed non-hot | providers | 2 | 13 | 0 |
| PT-079-providers-cloudflare | reviewed non-hot | providers | 1 | 3 | 0 |
| PT-080-providers-harness-execution | reviewed non-hot | providers | 1 | 5 | 0 |
| PT-081-providers-harness-runtime | reviewed non-hot | providers | 2 | 13 | 0 |
| PT-082-providers-shared-streaming | reviewed non-hot | providers | 1 | 1 | 0 |
| PT-083-sessions-compaction | reviewed non-hot | sessions | 2 | 4 | 0 |
| PT-084-sessions-harness | reviewed non-hot | sessions | 1 | 2 | 0 |
| PT-085-sessions-harness-runtime | reviewed non-hot | sessions | 7 | 112 | 0 |
| PT-086-sessions-harness-runtime | reviewed non-hot | sessions | 5 | 42 | 0 |
| PT-087-sessions-session-runtime | reviewed non-hot | sessions | 3 | 24 | 2 |
| PT-088-extensions-callbacks | reviewed non-hot | extensions | 4 | 52 | 0 |
| PT-089-tui-rendering | reviewed non-hot | tui | 1 | 1 | 0 |
| PT-090-cli-startup-modes | reviewed non-hot | cli | 8 | 50 | 3 |
| PT-091-cli-startup-modes | reviewed non-hot | cli | 2 | 8 | 1 |
| PT-092-utilities-support | reviewed non-hot | utilities | 8 | 92 | 0 |
| PT-093-utilities-support | reviewed non-hot | utilities | 1 | 8 | 0 |

## PT-001-tools-agent-loop

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/agent/test/agent-loop.test.ts` | 33 | partial | ported | `./agent` |
| `packages/agent/test/agent.test.ts` | 34 | partial | ported | `./agent`, `./coding` |
| `packages/agent/test/e2e.test.ts` | 10 | partial | ported | `./agent` |

## PT-002-tools-builtin-tools

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/agent-session-dynamic-tools.test.ts` | 4 | pending | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/bash-close-hang-windows.test.ts` | 2 | pending | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/builtin-tool-strict-mode.test.ts` | 3 | partial | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/default-tools-setting.test.ts` | 5 | pending | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/edit-tool-legacy-input.test.ts` | 8 | partial | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/file-mutation-queue.test.ts` | 7 | partial | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/suite/regressions/2835-tools-allowlist-filters-extension-tools.test.ts` | 2 | partial | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/suite/regressions/3302-find-path-glob.test.ts` | 4 | pending | ported | `./coding`, `./internal/codingagent/tools` |

## PT-003-tools-builtin-tools

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/suite/regressions/3303-find-nested-gitignore.test.ts` | 2 | pending | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/suite/regressions/3592-no-builtin-tools-keeps-extension-tools.test.ts` | 3 | pending | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/suite/regressions/5109-exclude-tools.test.ts` | 2 | pending | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/suite/regressions/5208-late-bash-output.test.ts` | 1 | pending | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/suite/regressions/5303-bash-output-truncation.test.ts` | 2 | pending | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/suite/regressions/5998-blocked-tool-terminate.test.ts` | 1 | pending | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/suite/regressions/6104-find-root-relativization.test.ts` | 11 | pending | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/suite/regressions/6162-extension-active-tools-next-turn.test.ts` | 3 | pending | ported | `./coding`, `./internal/codingagent/tools` |

## PT-004-tools-builtin-tools

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/suite/regressions/6596-taskkill-enoent.test.ts` | 1 | pending | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/suite/regressions/8935-parallel-preflight-abort.test.ts` | 1 | pending | ported | `./coding`, `./coding/extension/host/inproc`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/suite/regressions/9068-user-bash-fail-closed.test.ts` | 2 | pending | ported | `./cmd/pig`, `./coding`, `./internal/codingagent`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/tool-system-prompt-contributions.test.ts` | 2 | pending | ported | `./coding`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/tools.test.ts` | 84 | partial | ported | `./cmd/pig`, `./coding`, `./internal/codingagent/tools` |

## PT-005-tools-harness

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/agent/test/harness/tools.test.ts` | 25 | partial | ported | `./agent`, `./agent/harness/tools` |
| `packages/agent/test/harness/truncate.test.ts` | 9 | partial | ported | `./agent`, `./internal/codingagent/tools` |

## PT-006-tools-validation

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/validation.test.ts` | 9 | pending | ported | `./agent` |

## PT-007-providers-anthropic

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/anthropic-adaptive-thinking-models.test.ts` | 1 | pending | ported | `./ai` |
| `packages/ai/test/anthropic-eager-tool-input-compat.test.ts` | 4 | partial | ported | `./ai` |
| `packages/ai/test/anthropic-eager-tool-input-e2e.test.ts` | 3 | partial | ported | `./ai` |
| `packages/ai/test/anthropic-empty-thinking-signature-compat.test.ts` | 7 | partial | ported | `./ai` |
| `packages/ai/test/anthropic-force-adaptive-thinking.test.ts` | 6 | partial | ported | `./ai` |
| `packages/ai/test/anthropic-long-cache-retention-e2e.test.ts` | 2 | partial | ported | `./ai` |
| `packages/ai/test/anthropic-mid-conversation-effort.test.ts` | 7 | pending | ported | `./ai` |
| `packages/ai/test/anthropic-oauth.test.ts` | 3 | pending | ported | `./ai` |

## PT-008-providers-anthropic

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/anthropic-opus-4-8-smoke.test.ts` | 1 | pending | ported | `./ai` |
| `packages/ai/test/anthropic-sse-parsing.test.ts` | 13 | partial | ported | `./ai` |
| `packages/ai/test/anthropic-temperature-compat.test.ts` | 6 | pending | ported | `./ai` |
| `packages/ai/test/anthropic-thinking-binding-e2e.test.ts` | 1 | pending | ported | `./ai` |
| `packages/ai/test/anthropic-thinking-disable.test.ts` | 8 | partial | ported | `./ai` |
| `packages/ai/test/github-copilot-anthropic.test.ts` | 3 | partial | ported | `./ai` |
| `packages/ai/test/xiaomi-token-plan-ams-anthropic-empty-signature-smoke.test.ts` | 1 | pending | ported | `./ai` |

## PT-009-providers-azure

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/azure-openai-base-url.test.ts` | 16 | partial | ported | `./ai` |
| `packages/ai/test/azure-openai-tool-choice.test.ts` | 2 | pending | ported | `./ai` |

## PT-010-providers-bedrock

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/bedrock-convert-messages.test.ts` | 12 | partial | ported | `./ai` |
| `packages/ai/test/bedrock-credentials.test.ts` | 3 | pending | ported | `./ai` |
| `packages/ai/test/bedrock-custom-headers.test.ts` | 6 | partial | ported | `./ai` |
| `packages/ai/test/bedrock-endpoint-resolution.test.ts` | 9 | partial | ported | `./ai` |
| `packages/ai/test/bedrock-error-metadata.test.ts` | 9 | pending | ported | `./ai`, `./coding` |
| `packages/ai/test/bedrock-models.test.ts` | 3 | partial | ported | `./ai`, `./coding` |
| `packages/ai/test/bedrock-raw-stop-reason.test.ts` | 2 | partial | ported | `./ai` |
| `packages/ai/test/bedrock-redacted-reasoning.test.ts` | 6 | pending | ported | `./ai` |

## PT-011-providers-bedrock

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/bedrock-response-headers.test.ts` | 1 | pending | ported | `./ai` |
| `packages/ai/test/bedrock-thinking-payload.test.ts` | 13 | partial | ported | `./ai` |

## PT-012-providers-catalog-options

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/baseten-models.test.ts` | 5 | partial | ported | `./ai` |
| `packages/ai/test/fireworks-models.test.ts` | 19 | pending | ported | `./ai` |
| `packages/ai/test/image-model-data.test.ts` | 3 | pending | ported | `./ai`, `./cmd/gen-image-models` |
| `packages/ai/test/images-models.test.ts` | 6 | partial | ported | `./ai` |
| `packages/ai/test/interleaved-thinking.test.ts` | 4 | partial | ported | `./ai` |
| `packages/ai/test/max-thinking.test.ts` | 4 | partial | ported | `./ai` |
| `packages/ai/test/model-catalog-types.test.ts` | 3 | partial | ported | `./ai` |
| `packages/ai/test/model-data-validation.test.ts` | 11 | partial | ported | `./ai`, `./cmd/check-model-data` |

## PT-013-providers-catalog-options

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/models-runtime.test.ts` | 39 | partial | ported | `./ai` |
| `packages/ai/test/openrouter-cache-control-models.test.ts` | 1 | pending | ported | `./ai` |
| `packages/ai/test/together-models.test.ts` | 3 | partial | ported | `./ai` |
| `packages/ai/test/xiaomi-models.test.ts` | 2 | pending | ported | `./ai` |
| `packages/coding-agent/test/max-thinking.test.ts` | 2 | pending | ported | `./ai`, `./cmd/pig`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/model-catalog-refresh.test.ts` | 3 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |

## PT-014-providers-catalog-options

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/model-registry.test.ts` | 87 | partial | ported | `./ai`, `./coding`, `./internal/codingagent` |

## PT-015-providers-catalog-options

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/model-resolver.test.ts` | 51 | partial | ported | `./ai`, `./cmd/pig`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/model-runtime-modify-models-compat.test.ts` | 5 | partial | ported | `./ai`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/model-selector.test.ts` | 3 | partial | ported | `./ai`, `./coding`, `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/models-store.test.ts` | 5 | partial | ported | `./ai`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/agent-session-compaction-model-overrides.test.ts` | 4 | pending | ported | `./ai`, `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/suite/agent-session-model-extension.test.ts` | 18 | partial | ported | `./ai`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/6949-unavailable-scoped-model.test.ts` | 4 | pending | ported | `./ai`, `./coding`, `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/suite/regressions/6999-models-json-hot-reload.test.ts` | 1 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |

## PT-016-providers-catalog-options

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/suite/regressions/7153-scoped-models-refresh.test.ts` | 2 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/7209-model-selector-filter-resets-selection.test.ts` | 2 | partial | ported | `./ai`, `./coding`, `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/suite/regressions/7443-model-command-cached-match.test.ts` | 2 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |

## PT-017-providers-cloudflare

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/cloudflare-stream.test.ts` | 2 | pending | ported | `./ai`, `./coding` |
| `packages/coding-agent/test/model-runtime-cloudflare-compat.test.ts` | 2 | partial | ported | `./ai`, `./coding`, `./internal/codingagent` |

## PT-018-providers-credentials

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/github-copilot-oauth.test.ts` | 13 | pending | ported | `./ai`, `./internal/codingagent` |
| `packages/ai/test/kimi-coding-oauth.test.ts` | 6 | partial | ported | `./ai` |
| `packages/ai/test/oauth-auth.test.ts` | 12 | partial | ported | `./ai` |
| `packages/ai/test/openrouter-oauth.test.ts` | 13 | partial | ported | `./ai` |
| `packages/ai/test/xai-oauth.test.ts` | 11 | partial | ported | `./ai` |
| `packages/coding-agent/test/auth-storage.test.ts` | 26 | partial | ported | `./ai`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/model-runtime-auth-options.test.ts` | 11 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/model-runtime-credential-sync.test.ts` | 10 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |

## PT-019-providers-credentials

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/oauth-selector.test.ts` | 6 | partial | ported | `./ai`, `./coding`, `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/runtime-credentials.test.ts` | 5 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/5433-extension-oauth-prompt-input.test.ts` | 5 | pending | ported | `./ai`, `./coding`, `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/suite/regressions/6324-branch-summary-ambient-auth.test.ts` | 1 | pending | ported | `./agent`, `./ai`, `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/suite/regressions/7027-credential-refresh-hang.test.ts` | 6 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |

## PT-020-providers-google

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/google-raw-stop-reason.test.ts` | 6 | pending | ported | `./ai` |
| `packages/ai/test/google-shared-convert-tools.test.ts` | 8 | pending | ported | `./ai` |
| `packages/ai/test/google-shared-gemini3-unsigned-tool-call.test.ts` | 6 | partial | ported | `./ai` |
| `packages/ai/test/google-shared-retry.test.ts` | 3 | pending | ported | `./ai` |
| `packages/ai/test/google-shared-signed-empty-blocks.test.ts` | 4 | partial | ported | `./ai` |
| `packages/ai/test/google-thinking-disable.test.ts` | 9 | pending | ported | `./ai`, `./coding` |
| `packages/ai/test/google-thinking-level-map.test.ts` | 9 | partial | ported | `./ai`, `./coding` |
| `packages/ai/test/google-thinking-signature.test.ts` | 5 | partial | ported | `./ai` |

## PT-021-providers-google

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/google-vertex-api-key-resolution.test.ts` | 9 | pending | ported | `./ai` |

## PT-022-providers-mistral

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/mistral-http-transport.test.ts` | 8 | partial | ported | `./ai` |
| `packages/ai/test/mistral-raw-stop-reason.test.ts` | 3 | partial | ported | `./ai` |
| `packages/ai/test/mistral-reasoning-mode.test.ts` | 10 | partial | ported | `./ai` |

## PT-023-providers-openai-codex

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/openai-codex-cache-affinity-e2e.test.ts` | 1 | pending | ported | `./ai` |
| `packages/ai/test/openai-codex-oauth.test.ts` | 8 | partial | ported | `./ai` |
| `packages/ai/test/openai-codex-stream.test.ts` | 30 | partial | ported | `./ai`, `./coding` |

## PT-024-providers-openai-completions

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/openai-completions-cache-control-format.test.ts` | 4 | partial | ported | `./ai` |
| `packages/ai/test/openai-completions-empty-tools.test.ts` | 11 | partial | ported | `./ai`, `./coding` |
| `packages/ai/test/openai-completions-prompt-cache.test.ts` | 15 | partial | ported | `./ai` |
| `packages/ai/test/openai-completions-raw-stop-reason.test.ts` | 2 | partial | ported | `./ai` |
| `packages/ai/test/openai-completions-response-model.test.ts` | 3 | pending | ported | `./ai` |
| `packages/ai/test/openai-completions-retry.test.ts` | 3 | partial | ported | `./ai` |
| `packages/ai/test/openai-completions-thinking-token-budget.test.ts` | 10 | partial | ported | `./ai`, `./coding` |
| `packages/ai/test/openai-completions-tool-choice.test.ts` | 49 | partial | ported | `./ai` |

## PT-025-providers-openai-completions

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/openai-completions-tool-result-images.test.ts` | 3 | partial | ported | `./ai` |
| `packages/ai/test/openai-completions-vllm-priority.test.ts` | 2 | partial | ported | `./ai` |

## PT-026-providers-openai-responses

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/azure-openai-responses-reasoning-replay.test.ts` | 2 | pending | ported | `./ai` |
| `packages/ai/test/openai-responses-cache-affinity-e2e.test.ts` | 1 | partial | ported | `./ai` |
| `packages/ai/test/openai-responses-compat.test.ts` | 18 | partial | ported | `./ai` |
| `packages/ai/test/openai-responses-namespace.test.ts` | 5 | partial | ported | `./ai` |
| `packages/ai/test/openai-responses-reasoning-replay-e2e.test.ts` | 3 | partial | ported | `./ai`, `./coding` |
| `packages/ai/test/openai-responses-terminal-event.test.ts` | 9 | partial | ported | `./ai` |
| `packages/ai/test/openai-responses-tool-result-images.test.ts` | 4 | partial | ported | `./ai` |

## PT-027-providers-shared-streaming

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/abort.test.ts` | 41 | partial | ported | `./ai`, `./coding` |
| `packages/ai/test/cache-retention.test.ts` | 19 | partial | ported | `./ai` |
| `packages/ai/test/compat-env.test.ts` | 1 | pending | ported | `./ai` |
| `packages/ai/test/constrained-sampling.test.ts` | 6 | partial | ported | `./ai` |
| `packages/ai/test/context-overflow.test.ts` | 35 | partial | ported | `./ai` |
| `packages/ai/test/cross-provider-handoff.test.ts` | 2 | partial | ported | `./agent`, `./ai` |

## PT-028-providers-shared-streaming

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/empty.test.ts` | 120 | partial | ported | `./ai` |

## PT-029-providers-shared-streaming

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/env-api-keys.test.ts` | 7 | partial | ported | `./ai` |
| `packages/ai/test/error-body.test.ts` | 16 | pending | ported | `./ai` |
| `packages/ai/test/faux-provider.test.ts` | 23 | partial | ported | `./ai` |
| `packages/ai/test/fetch-option.test.ts` | 6 | pending | ported | `./ai` |
| `packages/ai/test/image-tool-result.test.ts` | 46 | partial | ported | `./ai` |
| `packages/ai/test/images.test.ts` | 3 | partial | ported | `./ai` |
| `packages/ai/test/lax-message-content.test.ts` | 1 | pending | ported | `./ai` |
| `packages/ai/test/node-http-proxy.test.ts` | 5 | pending | ported | `./ai` |

## PT-030-providers-shared-streaming

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/openrouter-cache-write-repro.test.ts` | 1 | partial | ported | `./ai` |
| `packages/ai/test/openrouter-images.test.ts` | 3 | partial | ported | `./ai` |
| `packages/ai/test/openrouter-reasoning-options.test.ts` | 7 | partial | ported | `./ai`, `./cmd/gen-models`, `./internal/modelgen` |
| `packages/ai/test/overflow.test.ts` | 19 | partial | ported | `./ai` |
| `packages/ai/test/pre-generation-error.test.ts` | 1 | pending | ported | `./ai` |
| `packages/ai/test/provider-error-body-regression.test.ts` | 5 | pending | ported | `./ai` |
| `packages/ai/test/providers.test.ts` | 29 | partial | ported | `./ai` |
| `packages/ai/test/reasoning-options.test.ts` | 3 | pending | ported | `./ai`, `./cmd/gen-models`, `./internal/modelgen` |

## PT-031-providers-shared-streaming

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/responseid.test.ts` | 11 | partial | ported | `./ai` |
| `packages/ai/test/retry.test.ts` | 19 | partial | ported | `./ai` |
| `packages/ai/test/sampling-options.test.ts` | 6 | partial | ported | `./ai` |

## PT-032-providers-shared-streaming

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/stream.test.ts` | 233 | pending | ported | `./ai` |

## PT-033-providers-shared-streaming

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/supports-xhigh.test.ts` | 27 | partial | ported | `./ai` |
| `packages/ai/test/text.test.ts` | 4 | pending | ported | `./ai` |
| `packages/ai/test/tokens.test.ts` | 31 | pending | ported | `./ai` |
| `packages/ai/test/tool-call-without-result.test.ts` | 30 | partial | ported | `./ai` |

## PT-034-providers-shared-streaming

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/total-tokens.test.ts` | 35 | pending | ported | `./ai` |

## PT-035-providers-shared-streaming

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/unicode-surrogate.test.ts` | 90 | partial | ported | `./ai` |
| `packages/ai/test/xai-responses.test.ts` | 11 | partial | ported | `./ai` |
| `packages/ai/test/xhigh.test.ts` | 3 | pending | ported | `./ai` |
| `packages/ai/test/zen.test.ts` | 1 | pending | ported | `./ai` |
| `packages/coding-agent/test/agent-session-dynamic-provider.test.ts` | 5 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |

## PT-036-providers-shared-streaming

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/cache-stats.test.ts` | 11 | partial | ported | `./ai`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/cache-warmer.test.ts` | 9 | pending | ported | `./ai`, `./coding`, `./coding/extension/host/inproc`, `./internal/codingagent` |
| `packages/coding-agent/test/footer-data-provider.test.ts` | 8 | partial | ported | `./ai`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/http-dispatcher.test.ts` | 5 | pending | ported | `./ai`, `./cmd/pig`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/sdk-stream-options.test.ts` | 9 | pending | ported | `./agent`, `./ai`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/3317-network-connection-lost-retry.test.ts` | 1 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/3982-message-end-cost-override.test.ts` | 1 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/5661-uppercase-header-values.test.ts` | 1 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |

## PT-037-providers-shared-streaming

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/suite/regressions/6019-explicit-provider-retry-message.test.ts` | 1 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/7572-provider-retry-settings-merge.test.ts` | 1 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/8964-extension-provider-streaming.test.ts` | 1 | pending | ported | `./ai`, `./coding`, `./internal/codingagent` |

## PT-038-sessions-compaction

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/agent-session-auto-compaction-queue.test.ts` | 6 | partial | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/agent-session-compaction.test.ts` | 5 | partial | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/branch-summarization.test.ts` | 4 | partial | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/branch-summary-extensions.test.ts` | 1 | partial | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/compaction-extensions.test.ts` | 8 | partial | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/compaction-serialization.test.ts` | 3 | pending | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/compaction.test.ts` | 28 | partial | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/interactive-mode-compaction.test.ts` | 6 | partial | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |

## PT-039-sessions-compaction

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/settings-manager-compaction.test.ts` | 12 | partial | ported | `./agent`, `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/suite/agent-session-compaction.test.ts` | 28 | partial | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/suite/regressions/3688-tree-cancel-compacting.test.ts` | 1 | pending | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/suite/regressions/5217-compaction-reason.test.ts` | 3 | partial | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/suite/regressions/6647-compaction-retries-transient-stream-drop.test.ts` | 5 | partial | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/suite/regressions/6768-copilot-compaction-base-url.test.ts` | 1 | pending | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/suite/regressions/7253-manual-compact-during-response.test.ts` | 1 | pending | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/suite/regressions/8989-fork-compaction-label-boundary.test.ts` | 1 | pending | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |

## PT-040-sessions-compaction

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/suite/regressions/9178-tree-during-compaction.test.ts` | 2 | pending | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/suite/regressions/9340-9777-auto-compaction-cancellation.test.ts` | 5 | pending | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/suite/regressions/pre-prompt-compaction-no-continue.test.ts` | 1 | pending | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/compaction` |

## PT-041-sessions-harness

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/agent/test/harness/compaction.test.ts` | 22 | partial | ported | `./agent`, `./agent/harness/compaction`, `./coding` |
| `packages/agent/test/harness/system-prompt.test.ts` | 3 | partial | ported | `./agent`, `./agent/harness`, `./internal/codingagent/prompts` |

## PT-042-sessions-persistence

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/session-cwd.test.ts` | 3 | pending | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/session-file-invalid.test.ts` | 1 | pending | ported | `./cmd/pig`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/session-info-modified-timestamp.test.ts` | 1 | pending | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/session-manager/build-context.test.ts` | 16 | partial | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/session-manager/custom-session-id.test.ts` | 12 | partial | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/session-manager/file-operations.test.ts` | 28 | partial | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/session-manager/labels.test.ts` | 9 | pending | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/session-manager/load-entries.test.ts` | 13 | pending | ported | `./coding`, `./internal/codingagent` |

## PT-043-sessions-persistence

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/session-manager/migration.test.ts` | 2 | pending | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/session-manager/save-entry.test.ts` | 1 | partial | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/session-manager/tree-traversal.test.ts` | 31 | partial | ported | `./coding`, `./internal/codingagent` |

## PT-044-sessions-session-runtime

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/agent-session-branching.test.ts` | 3 | partial | partial | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/agent-session-concurrent.test.ts` | 7 | partial | ported | `./agent`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/agent-session-retry.test.ts` | 6 | partial | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/agent-session-stats.test.ts` | 9 | partial | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/sdk-session-manager.test.ts` | 4 | pending | ported | `./coding`, `./coding/extension/host/subprocess`, `./internal/codingagent` |
| `packages/coding-agent/test/session-id-readonly.test.ts` | 6 | partial | ported | `./cmd/pig`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/session-selector-path-delete.test.ts` | 8 | partial | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/session-selector-rename.test.ts` | 3 | partial | ported | `./coding`, `./internal/codingagent`, `./tui` |

## PT-045-sessions-session-runtime

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/session-selector-search.test.ts` | 9 | partial | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/session-share.test.ts` | 1 | partial | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/startup-session-name.test.ts` | 1 | partial | ported | `./cmd/pig`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts` | 11 | partial | ported | `./cmd/pig`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/agent-session-boundaries.test.ts` | 27 | partial | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/agent-session-prompt.test.ts` | 17 | partial | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/agent-session-queue.test.ts` | 15 | partial | ported | `./agent`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/agent-session-retry-events.test.ts` | 15 | partial | ported | `./coding`, `./internal/codingagent` |

## PT-046-sessions-session-runtime

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/suite/agent-session-runtime.test.ts` | 12 | partial | partial | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/lax-message-content.test.ts` | 6 | pending | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/1717-2113-agent-session-event-settlement.test.ts` | 2 | pending | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/2860-replaced-session-context.test.ts` | 3 | pending | partial | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/3686-session-name-event.test.ts` | 3 | partial | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/5943-session-start-notify.test.ts` | 7 | pending | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/5996-session-name-newlines.test.ts` | 2 | pending | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/7497-session-discovery-symlink.test.ts` | 3 | pending | ported | `./coding`, `./internal/codingagent` |

## PT-047-sessions-session-runtime

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/suite/regressions/9789-context-handler-system-messages.test.ts` | 6 | pending | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/startup-session-rebind-duplicate-subscription.test.ts` | 1 | pending | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/tree-during-streaming.test.ts` | 1 | pending | ported | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/system-prompt.test.ts` | 14 | pending | ported | `./coding`, `./internal/codingagent`, `./internal/codingagent/prompts` |

## PT-048-extensions-callbacks

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/extensions-input-event.test.ts` | 9 | pending | ported | `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess` |
| `packages/coding-agent/test/extensions-runner.test.ts` | 50 | partial | ported | `./cmd/pig`, `./coding`, `./coding/extension`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/5080-signal-shutdown-extension-cleanup.test.ts` | 5 | pending | ported | `./cmd/pig`, `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/6260-inline-extension-naming.test.ts` | 4 | pending | ported | `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess` |
| `packages/coding-agent/test/suite/regressions/7193-event-bus-lifecycle.test.ts` | 1 | pending | ported | `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess` |
| `packages/coding-agent/test/suite/regressions/8237-node-sea-extension-loading.test.ts` | 1 | pending | designed-out | `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess` |

## PT-049-extensions-harness

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/agent/test/harness/prompt-templates.test.ts` | 5 | partial | ported | `./agent`, `./agent/harness` |
| `packages/agent/test/harness/resource-formatting.test.ts` | 2 | partial | ported | `./agent`, `./agent/harness`, `./internal/codingagent` |
| `packages/agent/test/harness/skills.test.ts` | 6 | partial | ported | `./agent`, `./agent/harness` |

## PT-050-extensions-loading-resources

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/extensions-discovery.test.ts` | 30 | pending | ported | `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess`, `./internal/codingagent` |

## PT-051-extensions-loading-resources

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/prompt-templates.test.ts` | 93 | partial | ported | `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess`, `./internal/codingagent` |

## PT-052-extensions-loading-resources

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/resource-loader.test.ts` | 42 | partial | ported | `./cmd/pig`, `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess`, `./internal/codingagent` |
| `packages/coding-agent/test/sdk-skills.test.ts` | 3 | pending | partial | `./cmd/pig`, `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess`, `./internal/codingagent` |
| `packages/coding-agent/test/resource-loader.test.ts` | 42 | partial | partial | `./cmd/pig`, `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess`, `./internal/codingagent` |
| `packages/coding-agent/test/sdk-skills.test.ts` | 3 | pending | ported | `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess` |
| `packages/coding-agent/test/skills.test.ts` | 28 | partial | ported | `./cmd/pig`, `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess`, `./internal/codingagent`, `./internal/codingagent/prompts` |
| `packages/coding-agent/test/suite/regressions/8423-extension-factory-failure.test.ts` | 2 | pending | ported | `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess` |
| `packages/coding-agent/test/suite/regressions/9540-extension-loader-lazy.test.ts` | 1 | pending | designed-out | `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess` |
| `packages/coding-agent/test/suite/regressions/extension-factory-cache.test.ts` | 4 | pending | ported | `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess` |

## PT-053-tui-input

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/interactive-mode-startup-input.test.ts` | 3 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/keybindings-migration.test.ts` | 3 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/tui/test/autocomplete.test.ts` | 36 | partial | ported | `./tui` |

## PT-054-tui-input

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/tui/test/editor.test.ts` | 192 | partial | ported | `./tui` |

## PT-055-tui-input

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/tui/test/input.test.ts` | 36 | partial | ported | `./tui` |
| `packages/tui/test/keybindings.test.ts` | 7 | partial | ported | `./tui` |
| `packages/tui/test/keys.test.ts` | 60 | partial | ported | `./tui` |

## PT-056-tui-input

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/tui/test/stdin-buffer.test.ts` | 59 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/tui/test/tui-cell-size-input.test.ts` | 2 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/tui/test/word-navigation.test.ts` | 19 | pending | ported | `./internal/wordsegmenter`, `./tui` |

## PT-057-tui-rendering

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/ansi-utils.test.ts` | 5 | partial | ported | `./internal/codingagent`, `./internal/codingagent/tools`, `./tui` |
| `packages/coding-agent/test/assistant-message.test.ts` | 12 | partial | ported | `./ai`, `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/collapsible-message-components.test.ts` | 3 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/custom-message.test.ts` | 1 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/edit-tool-no-full-redraw.test.ts` | 3 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/footer-width.test.ts` | 9 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/initial-message.test.ts` | 3 | partial | ported | `./cmd/pig`, `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/interactive-mode-anthropic-warning.test.ts` | 4 | pending | ported | `./internal/codingagent`, `./tui` |

## PT-058-tui-rendering

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/interactive-mode-assistant-diagnostics.test.ts` | 2 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/interactive-mode-bug-report-hint.test.ts` | 4 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/interactive-mode-clone-command.test.ts` | 2 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/interactive-mode-import-command.test.ts` | 6 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/interactive-mode-status.test.ts` | 33 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/interactive-mode-suspend.test.ts` | 3 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/interactive-mode-tree-navigation.test.ts` | 4 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/interactive-tui.test.ts` | 11 | partial | ported | `./internal/codingagent`, `./tui` |

## PT-059-tui-rendering

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/mermaid.test.ts` | 8 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/settings-selector.test.ts` | 4 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/suite/regressions/4167-thinking-toggle-pending-tool-render.test.ts` | 2 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/suite/regressions/5596-missing-theme-export.test.ts` | 1 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/suite/regressions/7731-tui-method-wrapping.test.ts` | 2 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/suite/regressions/8537-custom-message-tool-result-ordering.test.ts` | 3 | pending | ported | `./coding`, `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/suite/regressions/8611-thinking-toggle-pending-bash-output.test.ts` | 1 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/syntax-highlight.test.ts` | 8 | partial | ported | `./internal/codingagent`, `./tui` |

## PT-060-tui-rendering

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/theme-controller.test.ts` | 6 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/theme-detection.test.ts` | 11 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/theme-export.test.ts` | 2 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/theme-picker.test.ts` | 1 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/tool-execution-component.test.ts` | 23 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/tree-selector.test.ts` | 18 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/truncate-to-width.test.ts` | 6 | partial | ported | `./internal/codingagent`, `./tui`, `./tui/widthx` |
| `packages/coding-agent/test/trust-selector.test.ts` | 4 | pending | ported | `./internal/codingagent`, `./tui` |

## PT-061-tui-rendering

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/user-message.test.ts` | 3 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/tui/test/fuzzy.test.ts` | 14 | partial | ported | `./tui` |
| `packages/tui/test/latex.test.ts` | 20 | partial | ported | `./internal/latex`, `./tui` |
| `packages/tui/test/layout.test.ts` | 15 | partial | ported | `./coding/extension/host/subprocess`, `./tui` |

## PT-062-tui-rendering

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/tui/test/markdown.test.ts` | 81 | partial | ported | `./internal/codingagent`, `./tui` |

## PT-063-tui-rendering

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/tui/test/overlay-non-capturing.test.ts` | 44 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/tui/test/overlay-options.test.ts` | 24 | partial | ported | `./tui` |
| `packages/tui/test/overlay-short-content.test.ts` | 1 | pending | ported | `./tui` |
| `packages/tui/test/regression-overlay-cjk-boundary.test.ts` | 4 | pending | ported | `./tui` |
| `packages/tui/test/regression-regional-indicator-width.test.ts` | 5 | partial | ported | `./tui`, `./tui/widthx` |
| `packages/tui/test/regression-sigwinch-kill-eacces.test.ts` | 3 | partial | ported | `./tui` |
| `packages/tui/test/settings-list.test.ts` | 2 | partial | ported | `./tui` |
| `packages/tui/test/tab-width.test.ts` | 4 | pending | ported | `./tui` |

## PT-064-tui-rendering

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/tui/test/truncate-to-width.test.ts` | 16 | partial | ported | `./tui`, `./tui/widthx` |
| `packages/tui/test/truncated-text.test.ts` | 9 | partial | ported | `./tui` |
| `packages/tui/test/tui-overlay-style-leak.test.ts` | 2 | partial | ported | `./tui` |
| `packages/tui/test/tui-render.test.ts` | 28 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/tui/test/tui-shrink.test.ts` | 1 | partial | ported | `./tui` |
| `packages/tui/test/wrap-ansi.test.ts` | 19 | partial | ported | `./tui`, `./tui/widthx` |

## PT-065-tui-terminal

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/block-images.test.ts` | 9 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/clipboard-command.test.ts` | 4 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/clipboard-image-bmp-conversion.test.ts` | 1 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/clipboard-image-native-errors.test.ts` | 1 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/clipboard-image.test.ts` | 8 | pending | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/clipboard.test.ts` | 21 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/image-processing.test.ts` | 11 | partial | ported | `./internal/codingagent`, `./internal/imageprocessing`, `./tui` |
| `packages/coding-agent/test/image-resize-callers.test.ts` | 4 | pending | ported | `./cmd/pig`, `./internal/codingagent`, `./internal/codingagent/tools`, `./tui` |

## PT-066-tui-terminal

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/suite/agent-session-tool-result-images.test.ts` | 1 | partial | ported | `./coding`, `./internal/codingagent`, `./internal/imageprocessing`, `./tui` |
| `packages/coding-agent/test/tool-result-images.test.ts` | 7 | partial | ported | `./coding`, `./internal/codingagent`, `./internal/imageprocessing`, `./tui` |
| `packages/tui/test/bug-regression-isimageline-startswith-bug.test.ts` | 11 | partial | ported | `./tui` |
| `packages/tui/test/native-clipboard-linux.test.ts` | 10 | pending | ported | `./coding/extension/host/subprocess`, `./internal/nativeplatform`, `./tui` |
| `packages/tui/test/native-module-path.test.ts` | 2 | pending | ported | `./coding/extension/host/subprocess`, `./tui` |
| `packages/tui/test/native-platform.test.ts` | 4 | pending | ported | `./coding/extension/host/subprocess`, `./internal/nativeplatform`, `./tui` |
| `packages/tui/test/terminal-colors.test.ts` | 9 | partial | ported | `./internal/codingagent`, `./tui` |
| `packages/tui/test/terminal-image.test.ts` | 65 | partial | ported | `./coding/extension/host/subprocess`, `./tui` |

## PT-067-tui-terminal

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/tui/test/terminal.test.ts` | 21 | partial | ported | `./coding/extension/host/subprocess`, `./tui` |

## PT-068-cli-packages

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/git-update.test.ts` | 5 | pending | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/package-manager-ssh.test.ts` | 8 | partial | ported | `./cmd/pig`, `./coding/source`, `./internal/codingagent` |

## PT-069-cli-packages

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/package-manager.test.ts` | 123 | partial | ported | `./cmd/pig`, `./coding/packagecontent`, `./internal/codingagent` |

## PT-070-cli-packages

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/suite/regressions/7187-malformed-package-manifest.test.ts` | 1 | pending | ported | `./cmd/pig`, `./internal/codingagent` |

## PT-071-cli-rpc

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/rpc-jsonl.test.ts` | 4 | partial | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/rpc-prompt-response-semantics.test.ts` | 4 | partial | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/rpc.test.ts` | 18 | partial | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/5868-rpc-unknown-command-id.test.ts` | 1 | pending | ported | `./cmd/pig`, `./internal/codingagent` |

## PT-072-cli-settings

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/config.test.ts` | 16 | partial | ported | `./cmd/pig`, `./internal/codingagent`, `./internal/configvalue` |
| `packages/coding-agent/test/settings-manager-bug.test.ts` | 4 | partial | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/settings-manager.test.ts` | 46 | partial | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/3616-settings-inmemory-reload.test.ts` | 3 | pending | ported | `./cmd/pig`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/7829-invalid-settings-warning.test.ts` | 1 | pending | ported | `./cmd/pig`, `./internal/codingagent` |

## PT-073-cli-startup-modes

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/args.test.ts` | 83 | partial | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/export-html-skill-block.test.ts` | 4 | pending | ported | `./cmd/pig`, `./internal/codingagent`, `./internal/codingagent/export` |
| `packages/coding-agent/test/export-html-whitespace.test.ts` | 3 | partial | ported | `./cmd/pig`, `./internal/codingagent`, `./internal/codingagent/export` |
| `packages/coding-agent/test/export-html-xss.test.ts` | 9 | partial | ported | `./cmd/pig`, `./internal/codingagent`, `./internal/codingagent/export` |
| `packages/coding-agent/test/first-time-setup-fork.test.ts` | 1 | pending | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/first-time-setup.test.ts` | 8 | pending | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/print-mode.test.ts` | 3 | partial | ported | `./cmd/pig`, `./internal/codingagent` |

## PT-074-cli-startup-modes

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/sdk-openrouter-attribution.test.ts` | 13 | partial | ported | `./cmd/pig`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/stdout-cleanliness.test.ts` | 2 | pending | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/2023-queued-slash-command-followup.test.ts` | 1 | pending | ported | `./cmd/pig`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/2781-skill-collision-precedence.test.ts` | 4 | partial | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/2791-fswatch-error-crash.test.ts` | 1 | pending | ported | `./cmd/pig`, `./internal/codingagent`, `./tui` |
| `packages/coding-agent/test/suite/regressions/5724-sigterm-signal-exit.test.ts` | 1 | pending | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/6363-agent-settled-event.test.ts` | 3 | partial | ported | `./cmd/pig`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/7269-cli-end-of-options.test.ts` | 2 | pending | ported | `./cmd/pig`, `./internal/codingagent` |

## PT-075-cli-startup-modes

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/suite/regressions/7290-json-stream-linear.test.ts` | 1 | pending | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/7301-stalled-availability-refresh.test.ts` | 3 | pending | ported | `./cmd/pig`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/7911-json-stream-usage.test.ts` | 1 | pending | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/7925-toolcall-start-metadata.test.ts` | 1 | pending | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/8261-subagent-project-trust.test.ts` | 2 | partial | ported | `./cmd/pig`, `./coding/extension/host/subprocess`, `./internal/codingagent` |
| `packages/coding-agent/test/suite/regressions/8724-in-memory-fork-active-tool.test.ts` | 1 | pending | ported | `./cmd/pig`, `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/tools-manager.test.ts` | 8 | partial | ported | `./cmd/pig`, `./internal/codingagent`, `./internal/codingagent/tools` |

## PT-076-tools-harness-execution

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/agent/test/harness/execution-primitives.test.ts` | 23 | pending | ported | `./agent/harness/agentharness`, `./agent/harness/execution` |
| `packages/agent/test/harness/execution-tools.test.ts` | 9 | pending | ported | `./agent/harness/execution` |

## PT-077-tools-harness-runtime

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/agent/test/harness/runtime/drive-tools.test.ts` | 9 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |

## PT-078-providers-catalog-options

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/qwen-token-plan-models.test.ts` | 10 | partial | ported | `./ai` |
| `packages/ai/test/zai-coding-plan-models.test.ts` | 3 | pending | ported | `./ai` |

## PT-079-providers-cloudflare

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/cloudflare-ai-binding.test.ts` | 3 | pending | ported | `./ai`, `./coding/extension/host/subprocess` |

## PT-080-providers-harness-execution

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/agent/test/harness/execution-assistant.test.ts` | 5 | pending | ported | `./agent/harness/execution` |

## PT-081-providers-harness-runtime

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/agent/test/harness/runtime/drive-generation.test.ts` | 12 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |
| `packages/agent/test/harness/runtime/drive-retry.test.ts` | 1 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |

## PT-082-providers-shared-streaming

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/experimental-transcript-provider.test.ts` | 1 | pending | ported | `./ai`, `./coding`, `./internal/codingagent`, `./internal/experimental/services` |

## PT-083-sessions-compaction

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/compaction-extensions-example.test.ts` | 3 | pending | ported | `./coding`, `./coding/extension/host/subprocess`, `./internal/codingagent`, `./internal/codingagent/compaction` |
| `packages/coding-agent/test/trigger-compact-extension.test.ts` | 1 | pending | ported | `./coding`, `./coding/extension/host/subprocess`, `./internal/codingagent`, `./internal/codingagent/compaction` |

## PT-084-sessions-harness

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/agent/test/harness/context.test.ts` | 2 | pending | ported | `./agent/harness` |

## PT-085-sessions-harness-runtime

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/agent/test/harness/runtime/accept.test.ts` | 17 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |
| `packages/agent/test/harness/runtime/drive-public.test.ts` | 27 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |
| `packages/agent/test/harness/runtime/drive-reconcile.test.ts` | 10 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |
| `packages/agent/test/harness/runtime/drive-retry-deferred.test.ts` | 11 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |
| `packages/agent/test/harness/runtime/drive-structural.test.ts` | 27 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |
| `packages/agent/test/harness/runtime/drive-terminal.test.ts` | 5 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |
| `packages/agent/test/harness/runtime/harness.test.ts` | 15 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |

## PT-086-sessions-harness-runtime

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/agent/test/harness/runtime/lane.test.ts` | 12 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |
| `packages/agent/test/harness/runtime/progress.test.ts` | 7 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |
| `packages/agent/test/harness/runtime/reducer.test.ts` | 7 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |
| `packages/agent/test/harness/runtime/restore.test.ts` | 8 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |
| `packages/agent/test/harness/runtime/watch.test.ts` | 8 | pending | ported | `./agent/harness/execution`, `./agent/harness/runtime` |

## PT-087-sessions-session-runtime

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/experimental-session-directory.test.ts` | 3 | pending | pending | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/experimental-session-worker-lifecycle.test.ts` | 12 | pending | pending | `./coding`, `./internal/codingagent` |
| `packages/coding-agent/test/experimental-session-worker-manager.test.ts` | 9 | pending | ported | `./coding`, `./internal/codingagent`, `./internal/experimental` |

## PT-088-extensions-callbacks

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/git-merge-and-resolve-extension.test.ts` | 9 | pending | ported | `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess` |
| `packages/coding-agent/test/input-transform-streaming-example.test.ts` | 6 | pending | ported | `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess` |
| `packages/coding-agent/test/plan-mode-extension.test.ts` | 4 | pending | ported | `./cmd/pig`, `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess` |
| `packages/coding-agent/test/plan-mode-utils.test.ts` | 33 | pending | ported | `./coding`, `./coding/extension/host/inproc`, `./coding/extension/host/subprocess` |

## PT-089-tui-rendering

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/experimental-client-tui.test.ts` | 1 | pending | ported | `./internal/codingagent`, `./internal/experimental`, `./tui` |

## PT-090-cli-startup-modes

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/experimental-agent-controller.test.ts` | 4 | pending | ported | `./cmd/pig`, `./internal/codingagent`, `./internal/experimental/services` |
| `packages/coding-agent/test/experimental-cli-entry.test.ts` | 3 | partial | ported | `./cmd/pig`, `./cmd/pig-experimental`, `./internal/codingagent` |
| `packages/coding-agent/test/experimental-internal-process.test.ts` | 2 | pending | ported | `./cmd/pig`, `./internal/codingagent`, `./internal/experimental` |
| `packages/coding-agent/test/experimental-plugin-reload.test.ts` | 1 | pending | ported | `./cmd/pig`, `./internal/codingagent`, `./internal/experimental` |
| `packages/coding-agent/test/experimental-presentation-facets.test.ts` | 4 | pending | pending | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/experimental-radius-relay.test.ts` | 7 | pending | ported | `./cmd/pig`, `./internal/codingagent`, `./internal/experimental` |
| `packages/coding-agent/test/experimental-remote-runtime.test.ts` | 26 | pending | pending | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/experimental-server-lifecycle.test.ts` | 3 | pending | pending | `./cmd/pig`, `./internal/codingagent` |

## PT-091-cli-startup-modes

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/experimental-server-profile.test.ts` | 5 | pending | pending | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/experimental-slash-commands.test.ts` | 3 | pending | ported | `./cmd/pig`, `./internal/codingagent`, `./internal/experimental/services` |

## PT-092-utilities-support

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/ai/test/fireworks-model-generation.test.ts` | 4 | pending | ported | `./ai`, `./cmd/gen-models` |
| `packages/ai/test/generate-models-strict.test.ts` | 1 | pending | ported | `./ai`, `./cmd/gen-models` |
| `packages/coding-agent/test/config-value-migration.test.ts` | 3 | pending | ported | `./internal/codingagent` |
| `packages/coding-agent/test/format-resume-command.test.ts` | 8 | pending | ported | `./internal/codingagent` |
| `packages/coding-agent/test/git-ssh-url.test.ts` | 10 | pending | ported | `./coding/source`, `./internal/codingagent` |
| `packages/coding-agent/test/package-command-paths.test.ts` | 31 | partial | ported | `./cmd/pig`, `./internal/codingagent` |
| `packages/coding-agent/test/path-utils.test.ts` | 13 | partial | ported | `./internal/codingagent`, `./internal/codingagent/tools` |
| `packages/coding-agent/test/paths.test.ts` | 22 | pending | ported | `./internal/codingagent` |

## PT-093-utilities-support

| Upstream test file | Case sites | Baseline | Current | Target Go packages |
|---|---:|---|---|---|
| `packages/coding-agent/test/resolve-config-value.test.ts` | 8 | partial | ported | `./internal/codingagent`, `./internal/configvalue` |

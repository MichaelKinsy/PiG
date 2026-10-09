# `extension.X = any` aliases that cannot be typed: the remaining T9 rows of the extension-opaque-any family

Detector: `make ledger-check` on integrate-042 plus lg-help-10-ca (848 gaps). The family is being typed by the lanes that landed `a2a4dbaeee` (`Model = *ai.Model`) and `51340d7294` (`AgentMessage = agent.AgentMessage`), and `ledger-ca-r2` also types `ImageContent`, `Component` and `OAuthLoginCallbacks`. This note covers the aliases that stay `any` there and have no owner in `logs/row-owners.tsv`.

## Aliases and rows

| Go alias (`coding/extension/opaque_types.go`) | Concrete Go type today | Open rows |
|---|---|---|
| `SessionEntry` | `internal/codingagent.SessionEntry` (`coding/compaction_api.go:16` re-exports it) | `SessionBeforeCompactEvent.branchEntries` |
| `CompactionPreparation` | `internal/codingagent` | `SessionBeforeCompactEvent.preparation` |
| `CompactionResult` | `internal/codingagent` (and `rpcclient.CompactionResult`, a different wire struct) | `SessionBeforeCompactResult.compaction`, `CompactOptions.onComplete` call |
| `CompactionEntry`, `BranchSummaryEntry` | `internal/codingagent.BranchSummaryEntry` (`coding/session.go:1869`) | `SessionCompactEvent.compactionEntry`, `SessionTreeEvent.summaryEntry` |
| `TreePreparation` | `internal/codingagent` | `SessionBeforeTreeEvent.preparation` |
| `SessionManager`, `ReadonlySessionManager` | `internal/codingagent.Session` (the `*SessionManager`) | `ExtensionContext`, `ExtensionToolContext`, `ExtensionCommandContext` `.sessionManager` (3), `newSession` `options.sessionManager` (3) |
| `ModelRegistry` | `coding.ModelRegistry` | `ExtensionContext`, `ExtensionToolContext`, `ExtensionCommandContext` `.modelRegistry` (3), `ExtensionRunner.getModelRegistry` |
| `CustomMessage`, `CustomEntry` | `internal/codingagent.CustomMessageEntry` / `CustomEntry` | `MessageRenderer` and `EntryRenderer` call:0 (owner: ledger-coding-agent) |

## Why a plain retype does not work

`internal/codingagent` imports `coding/extension` (events, runner types). `coding/extension` cannot import the concrete session types without a cycle. The lanes that typed `Model` and `AgentMessage` could do it because those live in `ai` and `agent`, below `extension`.

## Decision needed (one of)

1. Move the session-tree value types (`SessionEntry` and the entry structs, `CompactionPreparation`, `CompactionResult`, `TreePreparation`) into a package below `coding/extension` (for example `coding/session/types`), re-export them from `internal/codingagent` and `coding` as aliases, and type the extension aliases to them. About 13 rows close, plus the extension event payloads become typed for Go extensions. Cost: a wide mechanical move; the subprocess wire JSON is unchanged because the structs keep their tags.
2. Record the alias-to-`any` as a reviewed representation for these names (the host hands the extension the concrete value, the SDKs carry it as opaque JSON). Pi's types are the same opaque values to a subprocess extension. This closes the rows with no Go change but leaves a Go in-process extension to type-assert.

Evidence for option 2 being faithful for wire clients: `coding/extension/host/subprocess/event_payload.go` serialises these payloads to JSON for every SDK; no SDK reads them as typed Go values.

`SessionManager`/`ReadonlySessionManager` additionally have a narrow consumer-owned Go interface (`extension.SessionManager` consumers read entries and the leaf); a reviewed representation to that interface is the same rule that closed `f90f273f1d` ("narrow consumer-owned parameter interfaces are reviewed representations").

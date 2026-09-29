# User message content

## Upstream contract

Pi 0.87.1 `packages/ai/src/types.ts:509-513` defines user content as a string or an array of text and image blocks. `packages/coding-agent/src/core/session-manager.ts:439-451` replaces null or missing content with an empty array but returns valid content unchanged. `packages/coding-agent/src/core/messages.ts:185-188` passes a user message through provider conversion. A string is not interchangeable with a text-block array at these boundaries.

## Go representation and callers

`agent.UserMessage.Content` uses the existing sealed `ai.UserContent` union. Use `ai.UserText` for strings, including empty strings. Use `ai.UserContentBlocks` for arrays. `AgentMessage.Clone` copies mutable array storage without changing the variant. `UserMessage.LLMMessage` retains the variant and gives an omitted Go value the existing empty-array behavior.

Session context, provider conversion, RPC encoding, forking text extraction, compaction, and interactive transcript readers consume both variants. Text-only readers use the existing `ContentBlocks` view without changing stored content. Array fixtures use the named array type without changing their data. The context-transform test expects the string supplied by its handler instead of the decoder's former fabricated block array.

The harness queue preserves a supplied user message when no images are added. Adding images converts nonempty string content to one text block and empty string content to no text blocks before appending images. This follows `packages/agent/src/harness/runtime/lane.ts:1480-1494`. Custom-message conversion still wraps strings in text blocks, unlike ordinary user-message conversion.

The wire has no new fields or compatibility format. SDKs already transport strings and arrays. Conversion and cloning remain synchronous. They own no task, callback, file, socket, or cancellation source. Session and harness tests use the existing owned cleanup paths. This change adds no transcript work to the TUI input loop.

## Evidence

- `coding/session_lax_content_upstream_test.go` ports all six upstream test sites, including direct `SendCustomMessage` with null content and all three malformed message-entry rows.
- `agent/user_content_test.go` checks exact JSON and provider variants for empty, ordinary, whitespace, Unicode, image-array, and large string inputs. It checks clone independence and omitted Go content after provider normalization.
- `coding/session_user_content_union_test.go` drives persistence, resume, model selection, and the next prompt with stopped, errored, and aborted old assistant messages, including empty prior content and orphaned tool results.
- `agent/harness/session/types_json_test.go:TestWritesMatchUpstreamJSONMembers` compares the entry write directly with the upstream fixture. It no longer decodes that fixture through PiG and normalizes away the string-or-array difference.
- `cmd/pig/rpc_user_content_union_test.go` checks RPC against the Session message shape for both variants.
- `agent/harness/runtime/user_content_union_test.go` checks queue variant retention, image addition, and caller immutability.
- `internal/codingagent/compaction/user_content_union_test.go` checks ordinary and custom message conversion separately.
- `test/parity/scenarios/session/17-session-lax-message-content.toml` compares extension and projection results with published Pi 0.87.1 in three exact-output pairs. The Go command also runs resumed-Session and RPC guards.
- `BenchmarkUserContentUnion` covers decode, clone, JSON encoding, and provider conversion at 0, 64, and 65,536 string bytes. CPU and allocation profiles belong with the lane evidence. No speed improvement is claimed.

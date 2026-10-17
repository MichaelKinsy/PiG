# Parked representation: ai:Content (lg-c4-ai-r3 a05d2b1d2)

Status: waiting for lead approval. Lane-added representations are parked until the lead approves them.

Proposed entry in `test/parity/interface-closure/autobind/representations.json`:

    "ai:Content": "ai.GeminiContent",

Lane rationale: `google-shared.ts` imports `Content` (and `Part`) from `@google/genai`. The engine's alias table is per package, so it reads `utils/text.ts`'s unexported local alias `type Content = TextContent | ImageContent | ThinkingContent | ToolCall` for that name. `convertMessages`' `Content[]` result is then judged against the wrong union. With the entry, `convertMessages` and its call row close (30 -> 28 gaps).

Reviewer concern: the key `ai:Content` applies to every `Content` reference in the ai package, not only the `@google/genai` import. It would equally rebind the `utils/text.ts` local union to `ai.GeminiContent`. A narrower fix resolves imported names by their import source (google-shared's `Content` is `@google/genai#Content`) before the package alias table. Approving the entry as written accepts that `ai:Content` always means the Gemini wire type. The same broad key was parked once already (ai-a-3).

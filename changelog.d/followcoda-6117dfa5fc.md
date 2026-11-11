### Added

- `registerMarkdownTransformer(transformer, { messageTypes })` chooses the surfaces a Markdown transformer runs on, as in Pi: `user`, `assistant` and `assistant-thinking` (the default), `custom` (custom messages without a renderer and expanded skill blocks), `summary` (expanded branch and compaction summaries) and `changelog` (What's New, `/changelog`, Extension Updates and the Update Available note). Skill blocks and summaries hand the transformer their content without the header, and custom, skill and summary messages drawn from a session entry carry its `messageId` and `timestamp`. The Go, Python, Rust and Lisp SDKs and Node extensions take the same option, and the built-in Mermaid transformer also renders diagrams in summaries.

### Changed

- **Breaking (Go API):** `(*Extension).MarkdownTransformer` takes the new variadic `...sdk.MarkdownTransformerOptions` after the transformer, as Pi's `registerMarkdownTransformer` takes its options. Before: `func(MarkdownTransformerFunc)`. After: `func(MarkdownTransformerFunc, ...MarkdownTransformerOptions)`. A call `ext.MarkdownTransformer(fn)` compiles unchanged and keeps the default surfaces; only a use of the method as a value (a variable, a field or an interface method with the old one-parameter signature) fails to compile. `pig upgrade` names such a use and the new signature.

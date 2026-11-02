### Changed

- `highlightCode` styles code that highlight.js fails on with the `mdCodeBlock` color, as Pi does, in the TUI and in the Go, Rust, Python and Lisp extension SDKs; before, only the Markdown theme's `highlightCode` did and a direct call left that code unstyled. The host call `highlight.code` has no `markdown` flag any more (the host ignores one an older SDK still sends), because `getMarkdownTheme().highlightCode` is `highlightCode`.

### Fixed

- Code that fails to highlight takes the code-block color, as in Pi: `highlightCode` returns it in the `mdCodeBlock` color in the Go, Rust, Python and Lisp extension SDKs when the host's highlighter fails or cannot be reached, and in the host for a theme that lacks a syntax color, where PiG panicked. `getMarkdownTheme().highlightCode` is `highlightCode`, so the `highlight.code` host call no longer has a `markdown` member. <!-- fix: 84b2fcadd1 -->

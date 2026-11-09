### Fixed

- The embedded Node runtime and the packed Go, Rust, Python and Lisp SDK bundles were not regenerated when `sendUserMessage` gained `expandPromptTemplates` and the Node theme gained `sourceInfo`, so shipped Node extensions saw no `ctx.ui.theme.sourceInfo` and the packed Lisp SDK rejected `:expand-prompt-templates`. They are regenerated, and the theme row now compares a real `SourceInfo`. <!-- fix: sdkhostgaps2 -->

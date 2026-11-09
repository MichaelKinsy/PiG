### Added

- A tool renderer in the Go, Rust, Python and Lisp SDKs receives `ToolRenderContext.lastComponent` (`LastComponent`, `last_component`, `tool-render-context-last-component`): what the renderer of the same phase of the same card returned on its previous run (its lines, or its kit view), nil on the first run and after a run that failed, as Pi's `lastComponent` is the component the renderer returned before. The Node runtime already passed Pi's component. The conformance suite binds its `render_probe` rows to the value in every SDK.

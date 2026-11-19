### Fixed

- The startup prompts (the project-trust and extension selectors, the extension text input and first-time setup) now load the `keybindings.json` of the agent directory, as Pi's startup UI does. They took whatever keybindings the process held before, so a remapped `tui.select.confirm` or `tui.select.cancel` was ignored by them. <!-- fix: ecfaad1026bf485d83930b69d152776fc8a52672 -->

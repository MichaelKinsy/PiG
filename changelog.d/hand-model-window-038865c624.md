### Changed

- Manual model switches (`SetModel`, `SetModelOnMain`, `BeginModelChange` from a user source, and user model cycling) are now refused before mutating session state when known context tokens exceed the target model's positive context window, prompting the user to run `/compact` first; fitting targets, unknown token counts, unknown (`0`) windows, and extension-triggered switches remain allowed.

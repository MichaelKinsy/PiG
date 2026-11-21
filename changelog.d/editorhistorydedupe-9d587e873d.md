### Fixed

- A prompt you submit again, or one restored when you switch branches (`/tree`, `/fork`, `/clone`, `/resume`), no longer appears twice in the up-arrow history; it moves to the front instead of duplicating older entries and pushing history past the 100-entry limit. Affects v0.2.0 through v0.7.0. <!-- fix: 9d587e873d --> <!-- cause: Editor.AddToHistory skipped only a consecutive repeat (inputHistory[0] == trimmed), matching Pi before commit 06a20368a7 (#12370), so re-adding an older prompt appended a duplicate. -->

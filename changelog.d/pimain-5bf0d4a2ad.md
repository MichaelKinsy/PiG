### Added

- `delta.AppendText(draft, key, text)` appends to a string in a draft and records the change as one append op without comparing the old and new strings (a plain `Set` of the longer string makes `Prepare` read the whole string). Ports Pi's `appendText`.

### Fixed

- Durable partial commits of a streamed answer no longer compare every growing text, thinking and tool-call argument string in full on each commit and re-assign the arguments snapshot: they append new text with `AppendText` and apply the argument changes of their own parse of the streamed argument text. <!-- fix: 5bf0d4a2ad -->
- An append op whose text starts with the low half of a surrogate pair joins the high half that ends the string it appends to, as JavaScript string concatenation does. <!-- fix: 5bf0d4a2ad -->

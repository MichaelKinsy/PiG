### Added

- In the terminal, Escape in a dialog whose extension declares a `dismiss` intent asks the extension to close it: the intent is raised with `{}`, and the dialog closes when the extension hides or ends its view. Escape asks once: a second Escape in a dialog that is still open returns the keys to the editor and folds the dialog into its row above the editor, as Escape does in a dialog without a `dismiss` intent, so Escape reaches the editor and interrupts within three presses, whatever the extension does.

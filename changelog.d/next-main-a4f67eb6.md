### Fixed

- `done()` of an overlay `ui.custom` component hides that component's own overlay instead of the topmost one. With a shell overlay open under a dialog overlay from another extension, the shell's exit no longer removes the dialog or leaves the finished shell on screen (Pi #10999, #10648).

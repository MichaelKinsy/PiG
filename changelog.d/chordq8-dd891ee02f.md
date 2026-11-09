### Changed

- Chord: a schema snapshot refuses a map keyed by anything but strings, a func, a chan and an unsafe pointer, naming the path (Go's form of Pi's symbol-key and accessor refusals); `surfaces.InLanguage`, `CurrentLanguage` and `Messages.InScope` carry the render scope in the context (Pi's `inLanguage`, `currentLanguage`, `t`).

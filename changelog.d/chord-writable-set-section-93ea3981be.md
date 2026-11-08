### Fixed

- Chord: a `Writable.Set` from outside a section of its owner's loop enters one, so its commit and the derives it causes run on the loop and never race the batch; a Set inside a section is the plain call. <!-- fix: b0ab4cd2563 -->

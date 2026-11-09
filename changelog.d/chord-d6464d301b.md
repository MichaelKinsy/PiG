### Fixed

- Chord: a plugin update's retry that a newer set replaced is cancelled even when the newer set arrives while the retry's timer is being set; the client kept the replaced set's timer pending. <!-- fix: d6464d301b5 -->

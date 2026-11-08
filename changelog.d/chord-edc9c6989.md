### Fixed

- Chord: a call that crosses a `failed` or a removal on the wire (sent before its consumer learnt of it) is answered with the cause its consumer was told (a removal's `unavailable`, a version refusal's `contract_mismatch`, a live failure's error), never `not_ready`. <!-- fix: edc9c6989 -->
- Chord: a call sent by the method indexes of a binding whose newer `bound` its consumer has not acknowledged yet is decoded by the names that binding had (another variant's indexes name other methods; one the new variant lacks fails `invalid/member`). <!-- fix: edc9c6989 -->

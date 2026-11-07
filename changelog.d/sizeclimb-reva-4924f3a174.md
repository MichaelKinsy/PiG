### Fixed

- `Value.Check` and `Value.Errors` of the Go, Rust, Python and Lisp extension SDKs follow TypeBox in three more cases. A schema keyword that has the wrong type (`{"minLength": "3"}`, `enum: 3`, `pattern: 5`, `multipleOf: "2"`, `maximum: null`) is skipped, so it accepts every value, as in Pi; it rejected values before, and `Errors` reported a limit that was empty. `const` and `enum` accept an object for an array that has the same indices and `length` (`{"0": "a", "length": 1}` equals `["a"]`). `uniqueItems` ignores an object's `constructor` member, so `{}` and `{"constructor": 2}` count as duplicates. <!-- fix: 4924f3a174, 62f99dccb6, 7071ef14e3 -->

### Changed

- `Value.Check` and `Value.Errors` do less work: `minLength` and `maxLength` of an ASCII text no longer convert the text, `uniqueItems` on strings and objects allocates up to 90% less and takes about half the time, and `Errors` builds its messages only for the checks that fail.

### Fixed

- `console.*` output and thrown non-Error values in Code Mode scripts print `NaN`, `Infinity` and `-Infinity` as `text()` prints them, not as `null` (so `throw NaN` reports `NaN`). A number nested in an object or array still prints as `null`, as `JSON.stringify` and `text()` print it (Pi #11901). <!-- fix: 62339c464299bc73839815a2821c62610ce15db8 -->

### Fixed

- A cross-harness agent plugin skill whose `description` is long in bytes but at most 1024 characters (for example 600 CJK characters) is no longer dropped from the package's skills. The limit counts UTF-16 units like Pi's skill validation, so 513 astral characters are over it. <!-- fix: f08ffe97f4 -->

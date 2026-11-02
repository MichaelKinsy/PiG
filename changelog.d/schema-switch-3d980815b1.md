### Fixed

- Validating arguments against a recursive schema whose object alternatives all reject a deeply nested value no longer takes exponential time: the call is rejected with its errors at once. <!-- fix: 3d980815b1 -->

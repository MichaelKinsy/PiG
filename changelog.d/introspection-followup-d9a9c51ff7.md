### Fixed

- `pig status --json` now gives a skill without a `description` and a theme that fails the theme schema `enabled: false` as well as the `health` text and the warning, so one field says whether the session has the resource. They were `enabled: true` before. They count as disabled in `resources`. <!-- fix: d9a9c51ff7 -->

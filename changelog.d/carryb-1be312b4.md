### Fixed

- Paths under a directory whose name merely starts with the home directory's (`/users/bob2/x` with home `/users/bob`) are no longer shown as `~2/x` in tool calls, the loaded-resource listing, the session selector and the tree selector; only a leading home directory, on a path-segment boundary, shortens to `~`. Carried forward from Pi 1be312b4 (earendil-works/pi#10488), past the 1.4.0+b3a29f11 pin. <!-- fix: 1be312b4 -->

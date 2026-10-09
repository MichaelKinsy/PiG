### Fixed

- A JavaScript or TypeScript extension loads when its `registerCommand` options carry a member Pi does not define, such as `args`. Pi ignores such members; PiG forwarded `args` to the host, which rejected a value that was not a string, and the extension failed to load.

### Changed

- The subprocess wire type `CommandDecl` no longer has the `Args` field. The host never read it, and Pi's `registerCommand` has no such option.

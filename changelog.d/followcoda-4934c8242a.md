### Added

- `pig auth login [provider]` and `pig auth logout [provider]` sign in to and out of a provider from the command line, as Pi's `pi auth login` and `pi auth logout` do, including the provider matching and the terminal question the login asks. The MCP sign-in hint now names `pig auth login <provider>`.

### Changed

- Consecutive cache warming notices merge into one line instead of stacking.

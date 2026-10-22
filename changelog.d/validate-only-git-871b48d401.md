### Fixed

- `pig install <source> --validate-only` now validates git and npm sources that are not installed yet. It fetches them into a temporary directory that is removed afterwards, and no longer reports `stat …/git/<host>/<owner>/<repo>: no such file or directory`. Installed packages and settings are not touched. <!-- fix: 871b48d4015ef3e7d99f6581ae3f7fa58698532e -->

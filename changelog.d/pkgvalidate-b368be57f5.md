### Fixed

- `pig install --validate-only [--json]` with no source now checks every local-path entry of `packages[]` (user and trusted project settings): it names each one whose directory does not exist with the path it resolved to and the `pig remove` command that drops it, and exits 1 when any is missing. Before, a Package whose folder was gone passed unnoticed (D216, #389). <!-- fix: b368be57f5 -->

### Fixed

- Preserve extension-authored per-run prompt sections through the Go, Node, Python, and Rust bridges. Session transcripts record section changes and remove them on a later unmodified run without changing the base prompt options.
- Keep an explicitly empty resumed prompt intact when no structured prompt or per-run section override replaces it.

### Fixed

- Load explicitly selected and extension-discovered skills, prompt templates and themes when `--no-skills`, `--no-prompt-templates` or `--no-themes` disables default discovery, as Pi does. Extension-discovered paths are trimmed and resolved against the working directory; an invalid path fails print and RPC startup instead of being dropped.

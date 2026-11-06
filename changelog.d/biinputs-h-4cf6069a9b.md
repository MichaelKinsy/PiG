### Fixed

- The read, write, edit and ls cards draw their call fallback, as Pi does, for a path argument whose `file:` URL cannot be resolved (a URL that names a host or holds an encoded `/`) when the terminal supports hyperlinks; they used to show the path unlinked. <!-- fix: 4cf6069a9b -->
- The read card's truncation warning prints a count the result does not carry as `undefined` and keeps a stored limit of 0, as Pi does; it printed `0` and the default limit. <!-- fix: 4cf6069a9b -->

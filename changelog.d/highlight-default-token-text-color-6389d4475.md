### Fixed

- Unscoped syntax-highlighted code (plain identifiers, operators, spaces, and punctuation) now takes the active theme text foreground instead of inheriting the terminal default foreground, keeping code readable when the terminal and PiG use contrasting themes. <!-- fix: 6389d4475 --> <!-- cause: cliHighlightTokens omitted the default highlight.js scope, so unscoped tokens emitted no foreground SGR and inherited the terminal default foreground. -->

### Fixed

- `--help` lists flags registered by loaded extensions in Pi's "Extension CLI Flags:" section, in extension load and registration order, and reports settings file diagnostics first on stderr, as Pi does. A flag without a description names its extension path, and flag and shortcut owners use the extension path rather than a built cell path.

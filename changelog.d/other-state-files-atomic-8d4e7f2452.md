### Fixed

- A full disk or a killed process while pig saved `keybindings.json`, `mcp.json` or the settings file during the legacy API-key migration could leave the file empty or cut short and lose your keybindings, your MCP servers or your settings. These files are now replaced as a whole through a temporary file in the same directory, so a failed write leaves the old file untouched. <!-- fix: 8d4e7f2452 --> <!-- cause: these files were written with os.WriteFile, which truncates the file before it writes, so a write that failed after it began left an empty or cut-short file. -->

### Changed

- A configured git or npm package that fails to install is now listed under `[Extension issues]` with the end of the command's output (`failed to install (<command> failed with code N: ...). Run "pig remove <source>" to remove it from settings.json.`), at startup and on `/reload`, as Pi lists it. The two startup lines `[pig] Reinstalled missing packages: ...` and `[pig] Could not install: ... (run pig update when online)` are gone: Pi prints neither, and the second one carried no error text.

### Fixed

- `/reload` and every session replacement now install the configured git and npm packages that are missing and refresh the temporary `-e` git and npm packages, as Pi's resource loader does on every load: `/new`, `/resume`, `/fork` and the RPC `new_session`, `switch_session` and `fork` as well as startup. Before, only startup installed (since v0.3.0; `/reload` never did since v0.2.0), so a package added to `settings.json` in a running session stayed missing until the next start. In the interactive TUI the install runs while the reload message is shown and git and npm output stays off the screen, and a failure is listed under `[Extension issues]`. <!-- fix: 451287c12e -->

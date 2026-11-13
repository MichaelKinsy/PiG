### Added

- Two CI guards protect the plugin UI work. `check-public-hygiene.py` rejects another organization's name, an event label, employer names outside copyright headers and listed person names in the UI paths. `make gui-import-guard` fails when chord imports a plugin UI package, so the dependency stays one-way. The same guard rejects any Wails import or module requirement: the plugin UI contract is host-agnostic, and hosts (TUI, web, MyGo, ACP, third-party) connect through adapters.

### Fixed

- `auth.json` keeps each credential's properties in the order they were read or returned by a login, as Pi does. Pi stores the object a login or refresh returned and writes it with `JSON.stringify`. Before, PiG rewrote every credential in a fixed key order whenever `auth.json` was saved, including credentials of providers that were not changed. A `config.oauth` login's credential gets its `type` after the flow's own properties, as Pi's `{ ...credential, type: "oauth" }` does.

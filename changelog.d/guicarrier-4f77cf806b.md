### Added

- RPC mode carries plugin views to a client outside the process (D614 wire carrier). A client attaches with `ui_view_attach`, is sent every mount as numbered `ui_view` snapshots, raises intents with `ui_view_intent` and cancels them by request id, reports what it could not draw with `ui_view_report`, and resyncs after a `ui_view_gap`. It works on stdin and stdout and on a `--listen` connection, and a failed carrier command carries a closed `code` beside the error text. A client that sends none of the seven `ui_view_*` commands sees no change. `docs/ui-view-carrier.md` is the page for client authors, with one transcript per message under `internal/viewcarrier/testdata/transcripts`.
- `examples/ui-view-web` is a reference web client for plugin views: a static page that draws mounts, applies their a11y and focus records and raises intents, and a bridge program (Node, standard library only) that relays RPC mode's lines to the page. Pig itself has no web server.

### Changed

- A plugin UI registry holds at most 8 attachments, clients of every kind together; a further `Registry.Attach` fails with `limit`.

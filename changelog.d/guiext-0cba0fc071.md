### Added

- Plugin UI mounts carry more, in the Go, Node, Python, Rust and Lisp SDKs alike (D614 amendment). A client states `claims`, and a mount offers up to 8 `alternatives`, each with a predicate over the claims; the host sends every client the first alternative that holds for it. A view can carry a `drawing` (a scene of vector shapes, or a pixel grid over an image) whose regions raise declared intents, and `a11y` and `focus` records that name view nodes by id. `finishView` takes a result, which the reply and `viewState` return. The new `viewDiagnostics` returns the notices of a mount: what the host rejected or ended and what a client could not draw. Every member is optional, closed and bounded, and none carries a version. One conformance row, `TestConformance_UIViewExtras`, runs the same probe against every SDK.
- A host can attach more than one client to the plugin UI registry. Each client holds an attachment with numbered full-snapshot deliveries, a gap and resync rule, and its own choice of alternative; `state.uiAbilities` is then the merge of the clients. Every delivery carries an RFC 8785 canonical SHA-256 digest of its content. `docs/extension-api-parity.md` states the ordering, cancellation and error rules.

### Changed

- `uiview.Registry.Subscribe` is replaced by `Registry.Attach`, and `UIBridge.SetClientAbilities` returns an error for abilities outside the record. Neither was in a release, so no migration is needed.

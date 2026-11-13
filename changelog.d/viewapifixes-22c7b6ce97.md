### Added

- Extensions are told when a viewer arrives, changes or leaves (D614): `pi.onViewAbilities((abilities, ctx) => ...)`, Go `Extension.OnViewAbilities`, Python `ext.on_view_abilities`, Rust `Extension::on_view_abilities`, Lisp `on-view-abilities`. The handler runs when the first client attaches, when the abilities change and when the last client leaves, and a mount it makes succeeds, so a client that attaches to an idle session is shown the extension's views without a prompt. With several clients attached the value is the merge of their abilities.
- `chordview.Views.Follow` mounts the chord panels when a client arrives.

### Changed

- An intent handler may redraw its own mount before it answers (D614). An update the answering handler makes to the mount its intent came from belongs to the answer: the intent settles `done` instead of `stale`, and RPC mode writes the `ui_view` event of that update before the reply to `ui_view_intent`. A change by another hand while the handler answers still settles the intent `stale`. No SDK call and no carrier message changed.
- `uiview.Registry.OnAbilities` registers any number of functions, in registration order, and returns the function that removes its argument; it replaced the function registered before.
- The demo extension of `examples/ui-view-web` redraws inside the answer; it no longer defers the update with a timer.

### Fixed

- `ctx.ui.viewAbilities()` of a Node extension reported no client after a session log sync until the next state update. <!-- fix: 1eea8f0691 -->
- `view_abilities()` of a Rust extension reported no client from its connect until the first state update, even with a client attached. <!-- fix: a47c8e87c7 -->
- Two clients that a mount's content does not fit, one shown the fallback and one shown no view, were sent the same digest (D614). Each is now sent the digest of its own content, and a client whose limits change so that it is owed the other content is delivered it; before, that delivery was taken for the one already made and the client kept what it drew. <!-- fix: 54bf1a5cfa -->

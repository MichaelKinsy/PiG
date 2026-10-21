### Fixed

- A Node extension's `ctx.ui.select`, `confirm` and `input` honor `options.signal`, as Pi's RPC and interactive modes do. A signal that is already aborted resolves the default (`undefined`, or `false` for `confirm`) without showing the dialog or writing an RPC request, and a signal that aborts while the dialog is open resolves the default at once and dismisses the dialog (RPC: the pending request is dropped). Before, the signal was ignored: the dialog stayed open until the user or its timeout answered, and the extension waited for it. <!-- fix: b2a73bfaed -->

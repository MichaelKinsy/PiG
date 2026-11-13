### Added

- A mounted view can carry a `title` (at most 64 bytes on one line), which a client uses as the label of its tab or of its row in a list of views. It is part of the content, so an update replaces it.
- An extension lets the user open a view it hid: one intent of a mount may be declared `whenHidden`, and it is the only intent a hidden mount takes. A client lists such a mount, and choosing it raises the intent; the extension's handler shows the view.
- A focus record can ask for the keyboard on an update (`request: "update"`) as well as at mount. The client decides; when it does not give the focus the extension reads an `unfocused` notice from `ui.view.diagnostics`.
- The three members are in the Go, Node, Python, Rust and Lisp SDKs with the same names and checks. A tool an extension registers can mount, show and focus its view; one conformance row proves it in every SDK.

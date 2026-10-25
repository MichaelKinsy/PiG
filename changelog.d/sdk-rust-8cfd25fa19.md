### Fixed

- A Rust extension that registers a flag after load (`Context::register_flag` from a handler) now reads its default through `Context::get_flag` before any value is set, as the Go, Node, Python and Lisp SDKs do and as Pi's `registerFlag` does. The Rust SDK kept flag defaults only from the load-time snapshot, so a flag registered later read `None`. <!-- fix: 8cfd25fa193ba8d5532b07c9900aea1adcae6514 -->

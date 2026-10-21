### Fixed

- RPC mode writes one `setWidget` request per `ctx.ui.setWidget` call from the Go, Python and Rust SDKs, in call order and before the command's response, as Pi does. A string list with no options went out as an unanswered frame that the host applied on a lane of its own: the last list a command set could be written after the command's response or after a later `notify`, a repeat of the same list was dropped, and a list set after a clear stayed cleared on the client. In RPC mode the SDKs now send the list as the host call every other widget shape uses. <!-- fix: 31e26eac2b -->

### Added

- A Go extension handler can read the key order of an event's objects with `Context.ObjectKeys(object)`, as a Pi handler reads `Object.keys`: the order the host wrote them. The Node, Rust, Python and Lisp SDKs already iterate objects in that order and need no call. (D273)

### Fixed

- A custom message keeps the member order of its `content` blocks everywhere the host holds it. `pi.sendMessage`, `appendCustomMessageEntry`, a session entry read back from a file and a custom message read from the wire took the content from a Go map, so the blocks of a content array (`{type, text}`, `{type, data, mimeType}`) were stored, written again and shown to extensions with their members sorted, for every SDK. <!-- fix: 402f61c2205adc5c2253437ad59cc8111d18d24c -->

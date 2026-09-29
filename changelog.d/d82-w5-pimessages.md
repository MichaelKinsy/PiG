### Fixed

- Fixed `pi-messages` streams reporting Go's JSON error text and `context canceled` instead of Pi's: a malformed backend record now fails the turn with V8's `JSON.parse` message, and an aborted request reports `This operation was aborted`.
- Fixed `pi-messages` inventing a `start` event for a backend that sends none. The stream now carries exactly the events the backend sent, as Pi's does, and a second `start` is forwarded.
- Fixed `pi-messages` response bodies being decoded byte-wise. Chunk splits inside a multi-byte character, a leading byte order mark, invalid UTF-8, `\r\n` record separators, and falsy JSON records now behave as in Pi's `TextDecoder` and `JSON.parse` path, and the 64 MiB SSE record limit that Pi does not have is gone.
- Fixed the assistant message a consumer observes at each `pi-messages` delivery differing from Pi's. The provider loop now spends the same number of microtasks per await, yield and rejection as Pi's `readPiMessagesEvents` and its `for await`, so `message_start` and every later event describe the message at the same point of the stream.

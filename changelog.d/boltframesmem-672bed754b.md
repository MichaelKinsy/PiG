### Fixed

- A `write` tool call that streams a large file no longer leaves the UTF-16 units of its last highlight warm-up in memory after the call is done: each finished card held up to 1.7 MB (a 400 KB file) in the highlighter until later warm-ups pushed it out. The units are released when the call completes, when its card is disposed, and by a warm-up that ends after either; the lines the card draws are unchanged. <!-- fix: 672bed754b67cb31d4a1d927e0ff63b2d16d05a4 -->

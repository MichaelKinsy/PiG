### Fixed

- Tool search ranks a tool first when the query is its name, with `-` and other non-identifier characters read as `_` and the `mcp__app__` prefix optional (`thread_read` and `app__thread_read` name `mcp__app__thread_read`). Before, BM25 length normalization could rank a tool with a short description sharing the name's words above it. <!-- fix: e16cbbaf76 -->

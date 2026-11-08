### Added

- p2's session tree, history, conversations and chat-mode plugins run on the Go host: `/tree` (branches, switching, labels, read-only earlier branches), the `history_search`, `history_read` and `history_handoff` tools, the `conversations` tool for talking to other sessions, and chat mode (mentions and the digest), with the chat outbox and session-start rules and the runtime's commit signal, owner links and session storage, as Pi's p2 plugins. <!-- feat: p2-chat-tree -->

### Fixed

- A send router or read-only check that reads the session (chat mode's router, the tree's earlier-branch check) no longer holds the worker's loop while a tool runs, which dropped the worker's link ("worker: link lost"). <!-- fix: p2-chat-tree -->

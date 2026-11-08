### Fixed

- `pig auth login` reports "Login cancelled." for any login that fails with that message, as Pi does, including the providers that create their own cancelled error.
- A custom message or a compaction summary drawn live (`pi.sendMessage`, an extension's boundary compaction) hands Markdown transformers no `messageId` or `timestamp`, as Pi does; only a restored transcript carries the entry's.
- Custom messages, skill blocks, summaries and the changelog run extension Markdown transformers off the render loop, so a slow extension no longer stalls a frame, and they are only redrawn each frame while an extension registered a transformer.
- A finished MCP sign-in whose single-use verifier could not be removed afterwards is still reported as authorized.
- MCP credential files that are symlinks are written through the link, and the directory is synced after the replace.

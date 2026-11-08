package sendfile

// toolDescription is the `send_file` tool's description, as upstream's tool.ts has it.
const toolDescription = `Offer a file for browser download. Path is relative to your cwd. Copies a private snapshot (maximum 25 MiB) and shows a Download link to all authorized viewers of this session. Downloads require a click; do not export secrets. Snapshots are retained.`

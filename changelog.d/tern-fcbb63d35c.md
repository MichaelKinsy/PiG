### Changed

- The Cloudflare Durable host's facade, types and tests follow Pi 1.3.0, as its core already did: tools return `{ output }`, and the facade pins Pi 1.3.0 packages. As in Pi 1.3.0, a tool that still returns the earlier `{ content }` shape produces an empty tool result.

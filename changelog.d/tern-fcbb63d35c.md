### Fixed

- The Cloudflare Durable host follows Pi 1.3.0 in its facade and types as its core already did: tools return `{ output }`, as in Pi 1.3.0, and the facade pins Pi 1.3.0 packages. A tool returning the Pi 1.0.4 shape `{ content }` produced an empty tool result.

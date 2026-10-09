# Isolated facet bridge assets

`chord/` contains the unmodified published JavaScript and source maps for the runtime closure of `@earendil-works/chord` 1.0.4 root, context, and Node loader exports, plus the `services/state-internals` and `services/provider` modules the driver imports. The files come from the exact Pi dependency locked in `extensions/sdk-ts/package-lock.json` (`node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/chord/dist/`). `make node-facets-chord` rewrites them from that dependency; `make node-facets-chord-check` (part of `make check-core`) fails when they differ. `chord/LICENSE` preserves upstream's MIT notice.

This is a private materialized module tree, not an installation or re-export of the whole Chord npm package. The separate Node package compiler is not included. PiG's server package builder uses the pinned esbuild Go compiler. Other host-provided externals require the caller's resolver or an installed resolvable module.

The original `chord/node/bundle-loader.js` SHA-256 is `dad8f67d063283847cf2a85e7bdcc87278e443e5418bcfe1e68c7752b6402e5c`. Do not edit the copied files. Run `make node-facets-chord` when the locked version changes.

The application adapter runs only inside the existing selected Node extension connection. It does not implement another process, socket, framing, heartbeat, or extension runtime.

Cancelling a request that awaits a retained Promise ends that observer through Chord's `awaitWithContext`. It does not cancel the producer Promise or consume its result. Another observer can continue waiting or read the settled result later. This applies to retained-value waits; command producer admission and generation ownership are separate contracts.

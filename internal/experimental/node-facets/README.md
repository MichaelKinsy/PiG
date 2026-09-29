# Isolated facet bridge assets

`chord/` contains the unmodified published JavaScript and source maps for the runtime closure of `@earendil-works/chord` 0.87.1 root, context, and Node loader exports. The files come from the exact Pi dependency under `extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/chord/dist/`. `chord/LICENSE` preserves upstream's MIT notice.

This is a private materialized module tree, not an installation or re-export of the whole Chord npm package. The separate Node package compiler is not included. PiG's server package builder uses the pinned esbuild Go compiler. Other host-provided externals require the caller's resolver or an installed resolvable module.

The original `chord/node/bundle-loader.js` SHA-256 is `dad8f67d063283847cf2a85e7bdcc87278e443e5418bcfe1e68c7752b6402e5c`. Do not edit the copied files. Replace this selected closure from the pinned published dependency when the upstream version changes.

The application adapter runs only inside the existing selected Node extension connection. It does not implement another process, socket, framing, heartbeat, or extension runtime.

Cancelling a request that awaits a retained Promise ends that observer through Chord's `awaitWithContext`. It does not cancel the producer Promise or consume its result. Another observer can continue waiting or read the settled result later. This applies to retained-value waits; command producer admission and generation ownership are separate contracts.

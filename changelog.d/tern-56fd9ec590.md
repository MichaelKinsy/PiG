### Fixed

- A Node extension that registers or unregisters a virtual model (`pi.registerVirtualModel`/`unregisterVirtualModel`) sees the write on the next line, as Pi does: `ctx.modelRegistry.find`, `getAll` and `getAvailable` list the model, a provider of only virtual models is configured and available at once, and an unregistration removes the model. The host also publishes the catalog when it serves either call, so another extension's registry sees the model. <!-- fix: 56fd9ec590 -->

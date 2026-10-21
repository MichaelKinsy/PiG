### Fixed

- The Node extension runtime's `ctx.modelRegistry.runtime` is now a model runtime that a nested Session can share. An extension that passes it to `createAgentSession({ modelRuntime })`, as the pi-goal-x completion auditor does, failed with `this._modelRuntime.hasConfiguredAuth is not a function`. The nested Session now uses the parent's providers and credentials, as in Pi. <!-- fix: be97501ff9 -->

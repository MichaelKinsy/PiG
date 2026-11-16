### Fixed

- The `root` of a TypeScript `PiGWireView` is typed against the node kinds the host builds (`PiGViewNode`) and no longer accepts any object: a misspelt kind, a member of another kind, a select-list without its id or a string `imageId` is a compile error and no longer a refused mount at run time. The plugin view API is unreleased and no release has the type. <!-- cause: PiGWireView.root was Record<string, unknown>, which no type test could fail --> <!-- fix: 194a2b1b70 -->

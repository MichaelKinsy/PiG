### Added

- chord: a web surface for an app with its own HTTP stack: `WebSurfaceOptions.Routes` false contributes no `Http` routes, and the surface gives `File(path, ctx)` (the file forever, 404, 503 when the build failed), `PageAssets(ctx)` and `Base(ctx)` for the app to serve with its own access rules and CSP; `PageBootstrap(hash, set)` is exported. `ClientOptions.Follow` false makes a static client (a page built offline and served elsewhere): it neither follows the scripts object, applies a set, nor asks the server's base, so it is never outdated. `startPage({ themes: false })` runs no kit themes and writes nothing on `:root`; `startPage({ follow: false })` is the static page (Pi 22fe46c7b).

### Changed

- chord: the vendored client tree (`chord/nextnode/surfaces/client`) is made from a derived Pi tree, the pin plus Pi 22fe46c7b, 3c771bd18 and da44c4df3 cherry-picked (manifest `carried`, `PI_CARRIED` of the vendoring script), until Pi's chord events (8d30e6306) are carried and the whole closure is re-vendored at a later commit.

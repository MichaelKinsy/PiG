### Added

- Go API `chord/nextnode/plugins` (Pi `c23b9ec1`): `*.module.css` files of a browser build are scoped to their plugin and file (`ScopeCSSModule`; classes `chord_<hash>_<file>_<class>`, `composes` and `@import` kept, `url()` of any stylesheet an asset), and the base marks: `Plugins.WatchBase(BaseInputs)` with `Recorder` (the hash of each file as a base build read it) sets `base` exactly while a recorded file differs, is gone, or a source file is new in a base directory (the server's own code is hashed at start). Starting the watcher over a base directory now reads each of its source files once (5.2 ms for 300 files).

### Fixed

- CSS module mappings are what Node's `vm` gives, as Pi's (key order of integer-like names, no `__proto__` key, template-literal names): `ScopeCSSModule` now takes the Node options and a context (the component's builds share one Node subprocess for the mappings), and `ScopedCSS.Classes` is the mapping's JSON text; a CSS module is decoded as UTF-8 like `TextDecoder`. A chmod of a recorded base file is compared like a write, and the base watches are renewed only when the directory is another one (inode and birth time), as Pi's. <!-- fix: 0197b91f2f -->

- On macOS (kqueue), the watches of a base directory moved away and back are put back, and a watch that cannot be put on one entry (a protected or vanished one in a watched directory) no longer stops every watch. <!-- fix: 0197b91f2f -->
- The watcher of the plugins component closes the system watcher outside the host's lock, so a failure to watch no longer holds the host while the system (kqueue: one watch per entry) is slow to close; `Plugins.Close` still waits for it. <!-- fix: 0197b91f2f -->

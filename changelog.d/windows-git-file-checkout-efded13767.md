### Fixed

- Installing a Package or Piglet from a local `git:file://` source on Windows no longer fails with `fatal: '$GIT_DIR' too big`. A file URL now checks out to `<install root>/localhost/<12 hex of the path's sha256>/<basename>` instead of re-nesting the whole absolute path, which pushed `$GIT_DIR` past Git for Windows' own path limit that `core.longpaths` does not lift. Sources Pi accepts keep Pi's exact layout. A file URL checkout made by an earlier version is not reused and is cloned again. <!-- fix: efded13767cd31c5d180aa24b1eb94a40e5d8696 -->

### Added

- A Piglet can put a strip list in keep mode with `strip.keep.<list>`: the list names the built-ins that stay, and every other built-in of that list is stripped, including built-ins a later PiG adds, so a minimal base stays minimal. `keep.extensions: []` keeps no built-in extension. One list is in deny mode or keep mode in one Piglet file, keep IDs are checked like deny IDs, keep mode is inherited down `extends`, a child can narrow but not leave keep mode, and `extends.remove.strip` re-enables single IDs in both modes with `extends.allowWiden`. `pig piglet show` prints a keep-mode list as `extensions  keep([])` above the expanded rows.
- Piglet Binary records keep the strip table of the PiG that built them (`stripTable`) and a keep-mode Piglet's keep lists (`stripKeep`). When a Binary of the Piglet was built before, `pig piglet build` and `pig piglet show` print a strip delta report: the built-ins this PiG adds since that build, which a keep-mode list leaves out and which a deny-mode list now includes.

### Fixed

- `pig piglet schema` lists the `apis` strip list.

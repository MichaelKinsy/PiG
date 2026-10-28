### Changed

- The docs bundle has a new page, `piglet-strip.md`, that explains how `tools` limits a Piglet, what the `strip` lists remove, and how to inspect the active Piglet. The README starts with four PiG-specific task rows and a hint to search `divergences.md` with `grep -n -i`. The README now puts the install and bundle details after the task table.
- The bundle pages `tui.md`, `sdk.md`, `themes.md`, and `custom-provider.md` link to bundle pages as `<page>.md`, and a drift test rejects root-absolute links in the bundle.
- The Additive features table in `divergences.md` is generated from `docs/additive-features.md` (`make additive-list`, part of `make generate`) and lists all additive records. `knowledge-graph.md` has rows for the slot, strip list, and overlay Piglet, and it no longer names paths that exist only in a source checkout.

### Added

- p2's ports plugin: the owner's port forwards (`Ports`, `ForwardStore`; fingerprints equal Pi's), `forwards.json`, the private and public Radius forward origins, and the `ports` tool with which a session's agent lists, opens and closes forwards (forwards of an archived session close). <!-- feat: p2-ports -->

### Fixed

- p2's session archive and copy jobs read their registries once the object composed them, so their first read no longer races the host's loop. <!-- fix: p2-ports -->

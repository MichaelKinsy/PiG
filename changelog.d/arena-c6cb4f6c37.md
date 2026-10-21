### Fixed

- Durable: opening a store that pi-durable 1.3.0 wrote no longer leaves you unable to go back to it. PiG's Durable writes schema version 2, which no Pi release reads yet, and it upgraded the store in place. `driver.OpenHarness` and `node.OpenNodeSqliteStorage` now copy a store at an older schema version to `<path>.schema-v1.bak` before upgrading it; pi-durable 1.3.0 opens that copy. An earlier copy is never replaced, and if the copy cannot be written the open fails and the store is left as it was (D188, #245).

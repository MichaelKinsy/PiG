### Changed

- Durable: opening a store that pi-durable 1.3.0 wrote now upgrades it in place, as Pi does, and no longer leaves a `<path>.schema-1.bak` copy beside it. PiG 0.5.2 and 0.5.3 made that copy because no Pi release read schema version 2 yet; pi-durable 1.4.0 writes version 2, so the upgraded store opens in the released Pi. Copies earlier versions already wrote stay where they are and can be deleted. `driver.OpenHarness` and `node.OpenNodeSqliteStorage` write no extra file. Divergence D188 is retired.

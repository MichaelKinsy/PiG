### Fixed

- p2's MCP plugin no longer restarts a server process it just started when two sessions lease locations close together: a late watch delivery used to reconcile an older placement over a newer one, closing an instance only the newer placement needs and starting it again. <!-- fix: 29d1d606c -->

### Fixed

- Chord: a registry value an app keeps past `host.close` (`host.registry`) no longer keeps the closed host alive; after the close it reads its last composition and holds nothing of the host. <!-- fix: c72462b3703acedca15f2c330cd05a01f75f22ea -->

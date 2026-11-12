### Fixed

- chord: a client starts without its server (a page renders offline, from a service worker's kept page): `StartClient` follows the scripts object in the background, a failure reported as `update_failed`, and `Client.Stop` ends and joins it; the set the scripts object shows when following begins is applied too, so a set published while the client started is no longer missed (Pi 3c771bd18). <!-- fix: a24f919546 -->

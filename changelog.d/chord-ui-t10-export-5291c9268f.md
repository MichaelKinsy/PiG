### Fixed

- A running worker's data export (`HostData.exportTo`) holds both of its stores at one point: the kit's store (`kit.sqlite`) keeps its commit queue from before the plugins' store (`store.sqlite`) is copied, SQLite copies that file at one instant, and both archives are written from those. It packed `store.sqlite` through a second handle and `kit.sqlite` after it, holding neither, so a write between the two showed in one archive only. <!-- fix: 5291c9268f -->
- `StartServer` refuses a worker kind without `Authorize` or `Start` with a start error instead of panicking at the first worker start. <!-- fix: 5291c9268f -->
- A worker's `.host-data.mjs`, which holds the start's secret token, is removed when the worker closes. <!-- fix: 5291c9268f -->
- The server no longer keeps a worker's telemetry host entry for every address it ever started: the entry ends when the workers component forgets the address. <!-- fix: 5291c9268f -->

### Added

- `workers.WorkersOptions.Forgotten`: called once an address has no worker, no operation and no failure left (Pi has no such hook; nothing observable changes).

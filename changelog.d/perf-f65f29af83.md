### Changed

- Switching the model registry, registering a provider whose models an extension wrote, and listing models for `/model` allocate far less when extensions registered models. An extension model's catalog record (Pi's spread of the definition) was encoded again for every model each time the registry rebuilt its entries; it is now encoded once per registration. Matching a model pattern no longer lowercases every model id and name per query, and the compaction and branch-summary cost rows no longer take big-number arithmetic for an exact tie such as 0.125.

### Changed

- The interface ledger reads `StoreStorage`: its `publishing` capability and `entry` overloads are renamed as the other storages' are, `StoredEntry` is a reviewed type exception, and four modules of `durable/storage/store` that Pi's entry point does not export carry a `// pi:` marker with a mutant that makes their tests fail. <!-- fix: 015bed2a37 -->

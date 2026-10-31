### Added

- Chord's store commits a series of commits in one backend transaction (`ChordStore.CommitSeries`): one seq and one outbox commit per commit that writes, the metadata once, each commit's check reading what the commits before it wrote. It replaces `CommitInBatch`. Durable's `StoreStorage` (`durable/storage/store`) keeps Durable's records in Chord's store tables and commits a batch list as one series (Pi e5c6182b).

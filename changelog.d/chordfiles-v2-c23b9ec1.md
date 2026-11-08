### Added

- Chord's app kit files (Pi main `bb5eafa8a`, `f617ed317`): `chord/nextnode/app` carries Files v2, the file storage of a host: spaces, paths and folders, versions that share blobs, keys that make a retried write store nothing, metadata queries, chunked uploads that resume, caps, a summary per space, `FilesFolder` listing one folder live, and `CopyFiles`, with the secrets and data directory layout they use. <!-- feat: files-v2 -->
- `chord/next/host`: `Reference.Start` and `PendingCall.Wait` start a call and await it apart, and `AwaitingCtx` runs the blocking remainder of an `onLive` routine from another package. <!-- feat: D430 -->

### Fixed

- Chord: a table read made while its object ended no longer races with the end. <!-- fix: tablehandle-assertlive -->

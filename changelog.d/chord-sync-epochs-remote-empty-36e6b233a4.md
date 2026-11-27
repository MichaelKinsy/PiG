### Added

- `chord/sync`: added `RemoteEmpty` (`UploadType = "remote_empty"`) so `UploadSession` reports when a remote lost its stored history (`PutNotHead` with `Head == LogStart`) and can restart from a full copy (library half only; PiG has no built-in caller of `UploadSession` outside `chord/sync`).

### Removed

- `chord/sync`: removed sync writer epoch fields and result variants (`StaleEpoch`, `PutStaleEpoch`, `UploadResult.Epoch`, `UploadOptions.Epoch`, `PutBatchResult.Epoch`, `PutBatchOptions.Epoch`), matching Pi `v1.6.1` (`a4c0976304`).

### Upgrading

- Callers of `chord/sync` that inspected `StaleEpoch`, `PutStaleEpoch`, `UploadResult.Epoch`, `UploadOptions.Epoch`, `PutBatchResult.Epoch`, or `PutBatchOptions.Epoch` must remove those references; sync writer epochs were retired in Pi `v1.6.1`.

### Fixed

- RPC mode: an extension dialog whose signal is already aborted now resolves its default without writing an extension_ui_request, as Pi does; the client no longer sees a dialog it cannot answer. <!-- fix: 6f9cf779f76ef93316e92d264a62b2ce084fc3dd -->

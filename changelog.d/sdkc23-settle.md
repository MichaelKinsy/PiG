### Fixed

- The Node extension runtime now serves Pi's `ModelRuntime.settleCredentialWrites` and pi-ai's `setBedrockProviderLoader`, and the Go, Rust, Python and Lisp SDKs gain `SettleCredentialWrites`/`settle_credential_writes`/`runtime-settle-credential-writes`, so an extension can wait for a started OAuth refresh to be saved before it exits. <!-- fix: sdk-surface-c23 -->

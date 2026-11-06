### Fixed

- Loading several source-based extensions (Go, Rust, Lisp or Node directories) hashes their sources together instead of one after another, so each Go extension no longer waits for the previous one to re-read the SDK it replaces: 16 Go extensions hash in 45 ms instead of 170 ms. Load and registration order are unchanged. <!-- fix: 87faba8e7d -->

### Fixed

- A turn that ends with a provider error that reports only a socket error code, such as `read ECONNRESET`, `write EPIPE` or `connect ETIMEDOUT`, or with `connection reset by peer`, is now retried automatically like other dropped connections, instead of stopping with the error; this is what Amazon Bedrock reports when a connection fails before a response. The same applies in the extension SDKs' `isRetryableAssistantError` (Go, Python, Rust, Lisp and the Node runtime). <!-- fix: 386b88c3b69 -->

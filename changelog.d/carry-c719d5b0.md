### Fixed

- A Zhipu/Z.ai usage limit (HTTP 429 with error code `1308`, which resets after hours) is no longer retried, in `ai.IsRetryableAssistantError`, the durable turn classifier and the Go, Rust, Python and Lisp extension SDKs. Carried forward from Pi c719d5b0 (earendil-works/pi#11537), past the 1.4.0+b3a29f11 pin. <!-- fix: c719d5b0 -->

### Fixed

- A turn that ends with a provider error reading `Connection closed.`, such as a gateway cutting a stream with an `api_error` event, is now retried automatically like other dropped connections, instead of stopping with the error; the same applies to the agent proxy's `Connection closed by proxy server before the response completed` error, in the extension SDKs' `isRetryableAssistantError`, and for durable agents (upstream #12093). <!-- fix: 9b5c69fa11e3f944140aa5c8965c562483f5b572 -->

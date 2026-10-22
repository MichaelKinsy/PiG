### Fixed

- The Cloudflare host's pi-durable facade sends Pi v1.4.0's client `identity` in every model request's options (last key, after `sessionId`), merged over `DEFAULT_HARNESS_IDENTITY` (provider attribution to pi) with `HarnessOptions.identity` and a request's own `identity`, as Pi's `Harness` does through `withClientIdentity` (Pi `adc5fa9f`, `04a93bad`). The facade exports `DEFAULT_HARNESS_IDENTITY`. <!-- fix: 4ff2bdc6de --> <!-- fix: d8e70b5b2a -->

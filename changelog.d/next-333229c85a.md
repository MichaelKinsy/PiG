### Fixed

- An HTTP 413 from any provider is treated as request-size overflow, so the agent compacts and retries instead of showing a raw API error. GitHub Copilot answers a request over its byte-size limit (for example a history of many images) with a 413 and a generic `failed to parse request` body that the token-count patterns never matched (Pi 73f8de80, #11016). <!-- fix: 333229c85a -->

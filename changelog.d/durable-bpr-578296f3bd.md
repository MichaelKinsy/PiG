### Added

- The durable core and its hosts run the `beforeProviderRequest` generation hook (Pi `d280e1ff`): a generation request sets the provider's `onPayload` as its last stream option, the host sends each request body to the core, and the core runs the replacement chain over it (a throw is reported and the chain continues) and answers with the body to send. Compaction requests do not use it. Both hosts (the JS facade and the Go driver) implement it; the ABI tables and `abi_id` are regenerated.

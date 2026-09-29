### Fixed

- Deliver the current assistant message to extension `message_update` handlers instead of reusing the message from `message_start`.
- Observe RPC events after awaited extension handling and before Session persistence, while retaining ordered event draining and response correlation.
- Preserve Agent shallow-message views through serialization, including provider tool-call scratch fields and independently owned encoded observations.

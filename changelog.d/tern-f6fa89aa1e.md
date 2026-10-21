### Removed

- `extension.CallLane` is removed from `github.com/MichaelKinsy/PiG/coding/extension`. It kept the calls of one destination in call order for `ToolDefinition.ReserveCallOrder`; the host now holds that order in an internal package, so there is no public replacement. `extension.CallOrder`, `extension.WithCallOrder` and `ToolDefinition.ReserveCallOrder` are unchanged: an embedder that reserved places with its own `CallOrder` values keeps working. `coding/mcpext.McpToolOptions.Lane` now has the internal lane type, so an embedder can no longer set it; leave it nil, which leaves the calls to that server unordered.

### Upgrading

- If your code builds an `extension.CallLane` or sets `mcpext.McpToolOptions.Lane`, stop doing so and reserve call order through `ToolDefinition.ReserveCallOrder` with your own `extension.CallOrder` values.

# Rows for the integrator (port-99-f6f-node)

`docs/extension-api-parity.md` and `docs/extension-sdk-surface.md`: the Node column of these rows is now realized (test in parentheses, all under `test/extension-conformance/node_extension_api_test.go` unless named):
- `pi.registerMcpServer`, `unregisterMcpServer`, `getMcpServers` (TestNodeMcpServerRegistration, TestNodeMcpServerRegisteredByTheFactoryIsCheckedAtLoad)
- `pi.registerVirtualModel`, `unregisterVirtualModel` (TestNodeVirtualModelRegistrationAndRouting, TestNodeVirtualModelRouteStateIsAbsentOrStored, node_session_twins_test.go TestNodeVirtualSuite*)
- `pi.getSettings` (TestNodeGetSettings)
- tool `outputSchema`, `exposure`, `namespace`, `annotations`, `defaultActive`, `prepareLoadout` (TestNodeToolExposureFieldsAndPrepareLoadout, TestNodeSessionToolOrchestration*)
- tool result `structuredContent`, `isError` (TestNodeToolResultCarriesStructuredContentAndIsError)
- `pi.getAllTools` exposure, namespace, annotations (TestNodeGetAllToolsCarriesExposureNamespaceAndAnnotations)
- `ctx.tools`, `ctx.executeTool`, `ExecuteToolOptions` (TestNodeExecuteToolAndCallableTools, ...OwnSignal..., ...DefaultsToTheCallingToolSignal)
- events `provider_stream_event`, `mcp_servers_change`, `parentToolCallId` (TestNodeToolEventsAndNewEvents)
- provider chat/image/classifier entries pass through (TestNodeProviderConfigWithImageAndClassifierModels)

Async contract for `ctx.executeTool` in the Node runtime (add to `docs/extension-api-parity.md` and `test/parity/async-contracts.toml`): the returned Promise resolves with the Host's outcome and never rejects for tool failures (they come back with `isError`); `onUpdate` runs synchronously from the runtime's receive loop for each partial result, in order, before the outcome resolves, and an error it throws rejects the call after the outcome; without `options.signal` the call is cancelled with the calling request, with one it is detached from the request and its abort sends `executeTool.cancel` after the call. `registerMcpServer`, `registerVirtualModel` and their unregister calls are synchronous (the runtime waits for the Host's reply) once the register frame is sent; before it they queue.

Known gaps recorded here: a partial result's `structuredContent` has no carrier in `agent.ToolUpdateCallback`, so the Host's agent never sees it (Node sends it on the wire). `TestPythonSDKVirtualModelRegistrationAndRouting` expects the Host to fail an unknown physical model; since dcd2035fb the model runtime rejects it (model-runtime.ts:1006-1009), so the Python row needs the correction the Node twin has.

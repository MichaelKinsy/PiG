### Added

- Child agent sessions in the Go, Python and Rust extension SDKs: `ctx.CreateAgentSession(sdk.CreateAgentSessionOptions{...})`, `ctx.create_agent_session(CreateAgentSessionOptions(...))` and `ctx.create_agent_session(CreateAgentSessionOptions { .. })` are Pi's `createAgentSession`. The host builds the child session over the parent's model runtime, so providers the parent registered reach it; `customTools` run in the extension that passed them; in-memory `settings` turn compaction off. The session has `prompt`, `steer`, `follow_up`, `abort`, `bind_extensions`, `state`, `messages`, `subscribe` and `dispose`, and ends with the extension's connection (its run is aborted and it is disposed). Calls block as Pi's Promises resolve: `prompt` returns once the run settled and every listener saw its last event. Node extensions already called Pi's own implementation. <!-- fix: cc584f5b -->
- The conformance rows for child sessions assert Pi's own observation, not PiG's: Node rows run Pi's `createAgentSession` through the sentinel, model-error and abort-mid-tool scenarios, and the Go (isolated, packed, fused), Python and Rust rows assert the same event lists, transcript roles, leading system message and error messages (`test/extension-conformance/testdata/child-session-want.json`). <!-- fix: 304bd443 -->

### Fixed

- A child session's `message_update` event carries the message so far and the assistant event's `partial`, as Pi's `AgentSession.subscribe` hands them to a listener, where RPC mode's trimmed shape left both out. <!-- fix: 304bd443 -->
- A streamed child-session text costs wire bytes linear in its length: `message_update` events travel as a keyframe followed by operations (`set` and `append`) and the SDK hands the listener the full event. A 4096-delta, 98 KB reply cost 406 MB on the connection and 826 MB of host allocation per prompt; it now costs 1.8 MB and 19 MB. <!-- fix: 304bd443 -->

### Upgrading

- Extensions written in Go, Python or Rust that fell back to a child `pig` process for `createAgentSession({customTools, settingsManager, modelRuntime})` can call the new API. `model` takes a model object, as Pi's option does: resolve a name through `ModelRegistry.Find` first. No existing API changes.

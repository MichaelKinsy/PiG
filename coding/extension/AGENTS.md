# Extension reliability

## Read first

- `../../docs/project/CONTEXT.md`
- `../../.upstream/current/packages/coding-agent/src/core/extensions/types.ts`
- `../../.upstream/current/packages/coding-agent/src/core/extensions/runner.ts`
- `../../docs/extension-api-parity.md`
- `../../docs/extension-runtime-cells.md`

## Contract

Every SDK exposes one extension contract. Isolated, packed, and fused extension realizations preserve that contract. External components keep their declared protocol and lifecycle. One Piglet can combine extension realizations and external components. Registration owns extension identity and capabilities. Reload and shutdown remove owned handlers and work.

## Failure modes

| Failure | Detection | Required result |
|---|---|---|
| One SDK returns a fallback instead of host state | Use a value distinct from every SDK default | Match the in-process reference |
| One extension realization has a weaker API | Run the same conformance row as isolated, packed, and fused | Match interface and behavior |
| One extension failure stops another topology | Crash an isolated extension, then call fused and packed tools | Keep healthy extensions available |
| Reload duplicates a handler | Subscribe, reload, and dispatch | Call each live handler once |
| Unsubscribe mutates active dispatch | Remove a handler during dispatch | Change only the next snapshot |
| A large extension set stalls startup | Load representative mixed extensions | Keep startup and registration bounded |
| Frames exhaust memory | Send oversized, malformed, partial, and stalled frames | Enforce bounds and cancel accurately |
| Shutdown leaks a process or socket | Cancel during host calls and streaming | Drain and close owned resources |

## Evidence

Define the cross-language row before implementation. Exercise Go, Rust, Python, and Node through real bridges. Exercise isolated, packed, fused, and mixed extension realizations. Test external components through their own declared protocol, not the extension SDK. Retain registration, frame, lifecycle, and crash evidence. Measure startup, reload, dispatch, memory, file descriptors, processes, and shutdown. Missing toolchains are environment failures.

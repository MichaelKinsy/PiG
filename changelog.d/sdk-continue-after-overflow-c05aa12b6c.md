### Fixed

- A `turn_end` or `agent_before_settle` handler that returns a `context_edit` draft without the `replacement` member is refused, as Pi refuses it ("Invalid boundary entries: Context edit replacement must be null or contain string/array content"): nothing is appended and the run does not continue. PiG took the absent member for null, left the target out of model context and continued. The cause: the host decoded an absent member and a null one into the same value. <!-- fix: c05aa12b6c -->

### Changed

- The extensions guide has the section "Own compaction and continue after an overflow": an extension that writes its own summaries continues a turn after a provider overflow with the result of its `turn_end` and `agent_before_settle` handlers (`entries`, `continue`) or with a `session_before_compact` result, without a second user message and without the failed attempt, as in Pi. Nine cases compare PiG with the pinned Pi package (`TestExtensionOwnedCompactionContinuesAfterOverflow`, parity scenario `session/38-overflow-owner-boundaries`), and one conformance row covers the null and omitted draft members in the Go, Node, Python, Rust and Lisp SDKs, isolated, packed and fused. The context's doc comment in each SDK points to the handler result.

### Upgrading

- Extensions: write `replacement: null` in a `context_edit` draft that omits an entry. A draft without the member is now refused.

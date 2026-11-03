### Fixed

- A tool's invocation-bound conversation handle rejects a `Submit` that queued behind another Session job when the invocation ended before the submit's job started, and stores nothing, as Pi's job-start abort check does; the handle's cancellation no longer detaches before the call queues (Pi session.ts commit). <!-- fix: cf6bb3ac45 -->
- A replay-safe tool's rerun shows its nested call's arguments in the member order of the stored checkpoint, not sorted (Pi tool.ts). <!-- fix: cf6bb3ac45 -->
- Expiring kept contexts stops at the first context still retained instead of copying the whole idle order on every settle, so the cost of a settle no longer grows with the number of idle contexts. <!-- fix: 64e88301d0 -->

# Extension wire: framing, queues, round trip and soak

The host talks to every extension process (Go, Node, Python, Rust) over one framed JSON connection: a 4-byte big-endian length, then the envelope's JSON (`coding/extension/host/subprocess/conn.go`). This page records what the connection guarantees, what is measured, and which gates hold it.

## Guarantees

| Property | Mechanism |
|---|---|
| Framing allocates nothing in steady state | An outbound frame is encoded into a pooled buffer that holds the length prefix and the JSON (`encodeFrame`), and returns to the pool after the write. An inbound frame is read into a pooled buffer (`readFrameBuffer`). |
| One socket write per small frame | The prefix and the payload travel together; a frame above `writeChunk` (64 KiB) is written in chunks so write progress counts as liveness while a large frame drains. |
| Bounded queues, with back-pressure | The writer queue holds 64 frames and the incoming queue 32. A full queue makes the sender wait for the writer; it never drops a frame, because a lost call result would leave the extension waiting forever. A peer that stops reading is closed by the heartbeat (D56). |
| Frame size cap | `MaxFrameSize`; an oversized frame is refused before it is built (`checkResultFrameSize`) and a result that is too large becomes an error frame. |
| Deadlines | Pi runs an extension handler with no wall-clock limit, so a request has none either. A hung extension is detected by the heartbeat (D56), a stalled handler by the inactivity lease (`HandlerStalledError`), and an aborted tool call gets the D111 grace period. |
| Stderr is always drained and bounded | Every extension process's stderr is copied into a log that keeps the first 1 MiB (a startup failure) and the last 64 KiB (a crash), and says how many bytes it dropped (`processStderrLog`). A flooding extension neither blocks on a full pipe nor fills the disk. |

## Measurements

Run on a shared build host with a load average far above its core count, so tail latency here is dominated by host scheduling, not by the SDKs. Compare runs from one machine only.

```sh
# Host side of the wire: allocations and time per frame, against a peer that allocates nothing.
go test ./coding/extension/host/subprocess -run '^$' -bench 'BenchmarkConn' -benchmem

# Open-loop round trip per SDK through the real host (fixed schedule, latency from the intended start).
go test ./test/extension-conformance -run TestExtensionRoundTripLatencyOpenLoop -v
PIG_EXT_RT_RATE=200 PIG_EXT_RT_SECONDS=30 PIG_EXT_RT_REPORT=$PWD/out go test ./test/extension-conformance -run TestExtensionRoundTripLatencyOpenLoop -v

# Random traffic with leak and latency budgets; the default is a six-second smoke test per SDK.
PIG_EXT_SOAK_SECONDS=7200 go test ./test/extension-conformance -run TestExtensionSoak -timeout 0 -v
```

Host-side cost of one frame (Go 1.27.2, Linux amd64, Xeon 6746E, loopback TCP):

| Benchmark | Before | After |
|---|---|---|
| `BenchmarkConnNotify` (encode, queue, write) | 20.2 µs, 125 B, 1 alloc | 8.6 µs, 25 B, 0 allocs |
| `BenchmarkConnRequestRoundTrip/empty` | 82 µs, 1787 B, 27 allocs | 62 µs, 1128 B, 13 allocs |
| `BenchmarkConnRequestRoundTrip/64KiB` | 766 µs, 158 KB, 28 allocs | 663 µs, 84 KB, 15 allocs |

The remaining allocations of a request are the per-request response and state channels (two objects each), the decoded response envelope and its fields, and the request bookkeeping. `json.Unmarshal` pools its decode state, scanner stack and error context (`extensions/sdk/json`), the frame length buffer is one per connection, the write-result channel is pooled (`writeResults`), and the operation name is built only for an error. The response and state channels are not pooled: a late frame for a finished request would reach the next request that reused the channel.

The pooled decode state is shared by the Go SDK, so each decoded message in a Go extension saves the same allocations.

Open-loop round trip of the scenario drivers' `noop` command, 100 calls per second for four seconds, p50 / p99 in milliseconds (three runs, ranges): Go 1.2–1.8 / 4–14, Rust 1.4–1.9 / 5–17, Node 1.5–3.1 / 14–780, Python 1.7–2.4 / 10–99. The p50 is stable. The p99 and above vary by two orders of magnitude between runs of one SDK on this host, which is host contention, so the default gate holds only a gross p99 bound.

Soak (six seconds, four workers, about 8,000 calls per SDK): no call fails, no goroutine or heap growth in the host, and no descendant process remains after shutdown. Resident memory of the extension process in a 200-second Node run (356,000 calls) ramps from 82 MiB to about 220 MiB in the first 40 seconds and then stays flat.

## Gates

| Gate | Test | Ratchet |
|---|---|---|
| Frame encode+queue allocations | `TestConnAllocationBudgets` (`sendAllocBudget`) | Fails above the budget and when two or more objects under it: lower the constant. |
| Request allocations | `TestConnAllocationBudgets` (`requestAllocBudget`) | Same. |
| One write per small frame | `TestSmallFrameIsOneSocketWrite` | Exact. |
| Large frames arrive intact | `TestLargeFrameChunksArriveIntact` | Exact. |
| Stderr bounded, tail kept | `TestStderrLogKeepsHeadAndTail`, `TestFloodingChildNeitherBlocksNorFillsTheDisk` | Exact bounds. |
| Round trip p50 | `TestExtensionRoundTripLatencyOpenLoop` (`roundTripP50BudgetMS`) | Budget in the test. |
| Round trip p99 | same, `PIG_EXT_RT_STRICT=1` on dedicated hardware (`roundTripP99StrictMS`) | Fails above the budget and more than 20% under it. |
| Soak leak and latency budgets | `TestExtensionSoak` | Constants at the top of `soak_test.go`. |

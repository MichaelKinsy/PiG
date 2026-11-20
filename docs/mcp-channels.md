# MCP channels contract (local additive PiG feature)

Channels are **untrusted server input, not user instructions or approval**. This
feature is an additive divergence from pi-mcp-adapter parity. It does not implement
permission relay (`claude/channel/permission`) or grant any tool permission.

## Wire contract and opt-in

Both gates are required, independently for each connection:

1. The MCP initialize result declares
   `capabilities.experimental["claude/channel"]` (normally `{}`). PiG checks the
   **presence of the key**, not the truthiness of its value.
2. The server's full `mcp.json` entry has `"channel": true`. Default, omitted,
   `false`, or `null` means off. Other non-boolean values are a config error.

```json
{
  "mcpServers": {
    "supervisor": {
      "command": "/path/to/channel-server",
      "args": [],
      "channel": true
    }
  }
}
```

This works in the agent-directory `mcp.json` and trusted-project full entries.
Existing project *partial overrides* still only allow `enabled`, `exposure`, and
`toolExposure`; use a full server entry to change channel opt-in. Extension-
registered servers do not acquire channel opt-in. Existing transport support
applies: stdio and streamable HTTP/SSE use the same notification handler.

PiG honours only the notification method `notifications/claude/channel`, with
params:

```json
{"content":"wake: inspect the latest result","meta":{"event_id":"42","kind":"wake"}}
```

- `content`: required JSON string, including the empty string. Missing, null, or
  non-string content drops the event silently.
- `meta`: optional object; omitted or null is treated as empty. A non-object
  drops the event silently. Each retained value must be a JSON string; individual
  non-string values (including null) are dropped.
- Meta keys must match **`^[A-Za-z0-9_]+$`**. Empty keys and other characters are
  dropped. The key `source` is reserved and dropped, preventing source spoofing.
- The complete raw JSON **params** must be at most **1,048,576 bytes**. Larger
  events are dropped, not truncated. There is no separate per-content/per-meta
  count limit within that budget. Transport-level framing limits also apply.
  There is no channel rate limit or queue-length cap; opt in only trusted servers.
- Unknown payload fields are ignored. Invalid payloads and events failing either
  gate are silently discarded (no user notification, turn, or session entry).

The handler is installed before initialization completes, so a push immediately
following `notifications/initialized` is not lost. Events before the initialize
result declares the capability are ignored. Server `instructions` continue to
use the existing MCP instructions path.

## Model text, persistence, and TUI

Exact model-visible text (no additional visible prefix):

```text
<channel source="SERVER" KEY="VALUE" ...>CONTENT</channel>
```

`source` is the configured server name, never supplied by the payload. Retained
meta attributes follow in lexicographic key order. Attribute values (source and
meta) are HTML-escaped with Go's `html.EscapeString`: `&`, `<`, `>`, `'`, and `"`
become `&amp;`, `&lt;`, `&gt;`, `&#39;`, and `&#34;`. Content between the tags
escapes only `&`, `<`, and `>` as `&amp;`, `&lt;`, and `&gt;`. Quotes and apostrophes
in content arrive verbatim. Thus `</channel>` in content becomes `&lt;/channel&gt;`
and cannot close the fixed wrapper. Plain text, including newlines, otherwise
remains unchanged.

Delivery uses a **custom message**, not `SendUserMessage`. In the agent transcript
its role is `custom` and its `customType` is `mcp-channel`. Provider conversion
presents custom message text as user-role model context, always with the fixed
wrapper above. **Provider role alone is not an approval/provenance signal.**

With session persistence enabled, each event has this distinct JSONL shape
(plus ordinary `id`, `parentId`, and `timestamp` fields):

```json
{"type":"custom_message","customType":"mcp-channel","content":"<channel source=\"supervisor\" event_id=\"42\" kind=\"wake\">wake: inspect the latest result</channel>","display":true,"details":{"source":"supervisor","meta":{"event_id":"42","kind":"wake"}},"mcpChannelSource":"supervisor"}
```

The host sets `mcpChannelSource` to the configured server name only after the
MCP adapter validates a push. `details.source` repeats the name for consumers;
`details.meta` contains retained, unescaped strings. An ordinary extension can
use the same `customType` or supply lookalike details and text without acquiring
channel attribution. A user typing the identical `<channel ...>` text still
persists as `type: "message"` with `message.role: "user"`, **not** as a
`custom_message`. Do not infer human approval from any of these fields, the text,
or the model/provider role. As with all other messages, `--no-session` disables
disk persistence.

The interactive TUI renders attributed pushes with user-message styling and a
first line `← SERVER`, live and when resuming the session. That marker is
presentation only; it is not added to the model text. Entries without host
attribution use ordinary custom-message rendering.

## Scheduling, ordering, and dedupe

- **Idle:** a received event starts a new model turn without any user prompt.
- **Busy:** synchronous admission to the steering queue; never aborts or injects
  into an in-flight model request or tool call. Delivery happens at the next
  ordinary between-turn steering boundary, after the current turn's tools finish.
- Adjacent pending pushes with host attribution are delivered as **one group**,
  FIFO, even when the ordinary steering mode is `one-at-a-time`. They remain
  separate custom messages and separate JSONL entries. Ordinary input,
  including same-name extension messages, separates groups and follows the
  existing queue policy.
- FIFO per server, including events sent immediately after initialization.
  Across servers, callbacks are serialized at one admission lock: their admitted
  arrival order is the delivery order. Concurrent arrivals have no independent
  wall-clock ordering guarantee.
- **No deduplication.** `meta.event_id` is ordinary metadata. A resent event is
  delivered again. There is no replay store, durable inbox, or exactly-once
  delivery guarantee; transport reconnect semantics are unchanged. Disabled,
  replaced, or shut-down connection attempts no longer admit events.

## Reproducible live probe

From the worktree root (writes only inside this worktree; no real API key/model,
no changes to `~/.local/bin` or the user's agent configuration):

```sh
go build -o "$PWD/bin/pig" ./cmd/pig
python3 scripts/probe-mcp-channels.py --pig ./bin/pig
```

The script contains a small stdio channel server, controlled through a Unix
datagram socket after MCP initialization, and a loopback OpenAI-compatible SSE
model fixture. It creates a temporary `mcp.json` with opted-in `push` and
non-opted-in `off` servers, then starts the **built PiG binary in RPC mode**.
It sends no user prompt: an idle push starts request 1, the fixture holds that
request while busy pushes arrive, and only releasing request 1 permits grouped
request 2. Assertions verify FIFO, dropped opt-out events, filtered meta, and
persisted provenance. It removes its temporary config/session files on exit.

For the server alone, run
`python3 scripts/probe-mcp-channels.py --server control.sock` with a normal MCP
stdio client; send JSON params to that relative Unix datagram path to push events.
The socket exists after `notifications/initialized`.

### Captured output (2026-10-27, before the host attribution field was added)

```text
idle: IDLE_WAKE started model request 1 without a user prompt
busy: model request 1 held; BUSY_1/BUSY_2 not delivered or interrupting
between turns: request 2 includes BUSY_1 then BUSY_2 as one group
opt-out: DROP_IDLE and DROP_BUSY absent from every model request
JSONL: {"content": "<channel source=\"push\" event_id=\"IDLE_WAKE\">IDLE_WAKE</channel>", "customType": "mcp-channel", "details": {"meta": {"event_id": "IDLE_WAKE"}, "source": "push"}, "display": true, "id": "d337b6bd", "parentId": "b2189197", "timestamp": "2026-10-27T17:11:00.557Z", "type": "custom_message"}
PASS: idle, busy FIFO/grouping, opt-out, meta filtering, distinct JSONL provenance
```

Tests: `coding/session_mcp_channel_test.go` uses a real Go test-helper stdio
server for idle delivery, busy delivery while a tool is blocked, both security
gates, cross-server ordering, and actual JSONL provenance versus identical user text.
`coding/mcpext/channel_internal_test.go` covers payload validation, limits,
escaping, meta filtering, config loading, and adapter custom-message routing.
`agent/queue_group_test.go` covers grouped FIFO without serialization leakage;
`internal/codingagent/interactive_mcp_channel_test.go` covers live and resumed
channel styling. The session and TUI tests also cover an ordinary extension
message named `mcp-channel`.

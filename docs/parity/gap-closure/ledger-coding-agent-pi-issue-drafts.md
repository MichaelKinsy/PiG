# Pi issue drafts for the five ahead-of-pi divergences without a Pi issue

The generated bug ledger prints "Pi issue: none recorded" for D99, D110, D111, D115 and D116. The generator reads the `Pi issue:` line of the divergence record (or `Pi #N` in its title or reason), so a link goes into `docs/parity/DIVERGENCES.md`, not into the generator.

Search on 2026-10-11 (GitHub search API, repo `earendil-works/pi`, titles and bodies, open and closed): no Pi issue describes any of the five defects. The nearest hits describe other defects, so none is linked:
- D110: #10061 (closed 2026-09-26) is `isLocalPath` treating `HTTPS://` as a local path. D110 (3) is `parseGitUrl` returning `https://HTTPS://host/org/repo`, a different function. #4530, #4851 and #4529 ask for subdirectory installs (feature requests; D110 (1) is additive).
- D111: #9910 (closed) is `emitToolCall` awaiting extension handlers without a timeout, not an aborted extension tool.
- D115: #5528 (closed) is Gemini 3.1 parallel tool results not collapsing into one turn. D115 is Gemini below 3 with an image in a non-last result. #2052 is Claude through Antigravity.
- D99, D116: no candidate.

Pi's contribution gate auto-closes pull requests without an approved issue (#8642), so the owner files the issues. Each draft below is complete enough to paste. When one is filed, add `Pi issue: https://github.com/earendil-works/pi/issues/N` to the divergence record and regenerate the ledger.

## D99 npm self-update on Windows fails while another process holds a loaded image open
`packages/coding-agent/src/utils/windows-self-update.ts:75-83` quarantines each loaded image with one `renameSync`. An anti-virus scanner or search indexer that opens a newly written or started executable without delete sharing makes the rename fail with EPERM or EBUSY, and the update fails. Expected: retry the rename briefly (npm's graceful-fs does) before failing. Pi 1.1.0.

## D110 `parseGitUrl` returns repositories no Git client can clone
Pi 1.1.0 `packages/coding-agent/src/utils/git.ts` `parseGitUrl`: `git://github.com/o/r` gives `https:////github.com/o/r` and `HTTPS://github.com/o/r` gives `https://HTTPS://github.com/o/r`, so `pi install` fails at clone time. (#10061 fixed only `isLocalPath` for the uppercase scheme.) Expected: the repository the user wrote.

## D111 an aborted extension tool call that never settles holds the agent loop
`packages/agent/src/agent-loop.ts:831-853` `executePreparedToolCall` awaits `tool.execute` without a limit. An extension tool that ignores its AbortSignal keeps the loop and the session until the process ends, and the user cannot recover the session. Expected: abandon the call after a grace period and report an aborted result.

## D115 Gemini below 3: parallel tool results with an image split the function responses
`packages/ai/src/api/google-shared.ts` `convertMessages`, `toolResult` branch: for models without multimodal function responses (major version below 3) the image turn is pushed right after its result. A parallel batch whose non-last result carries an image puts one model turn's function responses in separate user turns, and Gemini answers every later turn with HTTP 400 ("number of function response parts is equal to the number of function call parts"). Expected: images after the whole run of function responses.

## D116 Bedrock Converse can send two user messages side by side
`packages/ai/src/api/bedrock-converse-stream.ts:1014,1087,1124` push every user message, tool-result run and assistant message as its own message without checking the previous role. A prompt after a turn aborted while tools ran gives `assistant(toolUse), user(toolResult), user(prompt)`; Bedrock rejects every later turn with a ValidationException (HTTP 400). Expected: merge adjacent messages of one role.

The three sections below are drafts for divergences recorded on 2026-11-13 and 2026-11-17 (D632, D633, D146). No search for an existing Pi issue was made for them yet.

## D632 Overlapping `reload()` calls of `DefaultResourceLoader` end in a mix of both reloads
Pi 1.5.2 `packages/coding-agent/src/core/resource-loader.ts:541-710`: `reload()` resolves the skill, prompt template and theme paths before it awaits the extension load (`:585-590`, `:618`) and assigns them after it (`:635`, `:650`, `:669`), then reads the context files and the system prompt (`:678-705`). A second `reload()` started while the first waits in the extension load runs to its end; when the first resumes it assigns the paths it resolved before the await. To reproduce: change the configured skill directory and `SYSTEM.md` between two overlapping `reload()` calls whose first extension factory awaits; the loader ends with the first reload's skills and the second reload's system prompt. Expected: the state of one of the two reloads, for example by chaining `reload()` calls or by resolving the paths after the extension load.

## D633 A failed settings write is reported only at a later start, not where the setting was changed
Pi 1.5.2 `packages/coding-agent/src/core/settings-manager.ts:576-588`: a setter changes the value in memory and queues the write; a write that fails (for example `ELOCKED` while another process holds the lock, or a read-only file) is recorded for `drainErrors()` (`:561-563`, `:674-678`) and the setter returns. The callers go on as if the save had worked: `/settings` applies the change, `set_steering_mode` and `set_follow_up_mode` answer success (`modes/rpc/rpc-mode.ts:540-548`), `pi install` prints `Installed <source>` and exits 0 (`package-manager-cli.ts:1061-1063`). The user sees the failure only as a settings warning at a later start, and the changed value is gone after the restart. To reproduce: hold `settings.json.lock`, change a setting in `/settings`, restart. Expected: the failed save is shown at the point of the change (a status line, an error response, a non-zero exit).

## D146 Two session-replacement commands in RPC mode overlap, and `session_shutdown` goes to the wrong sessions
Pi 1.5.2 `packages/coding-agent/src/modes/rpc/rpc-mode.ts:833` starts every stdin line with `void handleInputLine(line)`, so a second `new_session`, `switch_session` or `fork` starts while the first still awaits (`:456-463`, `:625-635`). `AgentSessionRuntime` does not serialize them: `teardownCurrent` (`core/agent-session-runtime.ts:167-178`) acts on `this.session`, whichever session is current when the second command starts, and `apply` (`:180-185`) overwrites `_session` without disposing the previous one. Each command then calls `rebindSession()` (`rpc-mode.ts:321-323`), which binds whichever session is current by then. With an extension that logs `session_start` and `session_shutdown`: (1) with both commands in one write, the outgoing session gets `session_shutdown` twice and the first replacement's session never gets one; (2) the final session gets three `session_start` events; (3) with the second command written once the first replacement's `session_start` handler has been entered (the handler holds for one second), that session gets `session_shutdown` and is disposed under the running handler, and a handler that uses its captured `pi` afterwards (`pi.appendEntry`) produces an `extension_error` "This extension ctx is stale after session replacement or reload..." (`core/extensions/runner.ts:729-740`, `core/agent-session.ts:1477-1488`); (4) when the first replacement's `session_start` handler takes longer than the second's, the second command is answered first. To reproduce, start `D146_LOG=/tmp/d146.log pi --mode rpc --session /tmp/d146/s0.jsonl --session-dir /tmp/d146/sessions -e ./probe.mjs` and write `{"id":"a","type":"new_session"}` and `{"id":"b","type":"new_session"}` to stdin in one write (or the first line, then the second once the log shows the first replacement's `session_start` handler has been entered; `new_session` + `switch_session` and `fork` + `new_session` behave the same). `probe.mjs` logs the events and waits one second in the first `session_start` that is not the startup one, then calls `pi.appendEntry("late", {})`. Expected: each session gets one `session_shutdown`, the final session is bound once per replacement, no handler runs on a disposed session, and the answers come in the order sent. A fix could chain replacement commands in `rpc-mode.ts` or guard `AgentSessionRuntime` against a second replacement while one runs. The active session and the session files afterwards are the expected ones in every run, so nothing is lost at the end.

```js
import { appendFileSync, existsSync, writeFileSync } from "node:fs";
const log = process.env.D146_LOG;
export default function (pi) {
	const rec = (o) => appendFileSync(log, JSON.stringify(o) + "\n");
	pi.on("session_start", async (event, ctx) => {
		rec({ ev: "session_start", reason: event.reason, phase: "enter", sid: ctx.sessionManager.getSessionId() });
		let held = false;
		if (event.reason !== "startup" && !existsSync(log + ".held")) {
			writeFileSync(log + ".held", "1");
			held = true;
			await new Promise((r) => setTimeout(r, 1000));
		}
		rec({ ev: "session_start", reason: event.reason, phase: "exit" });
		if (held) pi.appendEntry("late", { n: 1 });
	});
	pi.on("session_shutdown", async (event, ctx) => {
		rec({ ev: "session_shutdown", reason: event.reason, sid: ctx.sessionManager.getSessionId() });
	});
}
```

## D660 The post-reply compaction check compacts after every reply when the model's context window is unknown
Pi `packages/coding-agent/src/core/agent-session.ts:2918` (main: `:3135`): `_checkCompaction` reads `const contextWindow = (messageModel ?? this.model)?.contextWindow ?? 0;` and `:3034` (main: `:3251`) decides with `shouldCompact(contextTokens, contextWindow, settings)`, which is `contextTokens > contextWindow - settings.reserveTokens` (`compaction/compaction.ts:267-270`). For a model whose window is 0 (a provider registered by an extension without `contextWindow`, or a model built with `contextWindow: 0`) that is true after every reply that carries usage, so every turn ends with a threshold compaction. `_exceedsCompactionThreshold` (`:741`, main `:833`) guards the same case with `if (model.contextWindow <= 0) return false;`, so the pre-turn check skips such a model and the post-reply check does not. Expected: the post-reply check skips a model whose window is not known, as the pre-turn check does. No search for an existing Pi issue was made.

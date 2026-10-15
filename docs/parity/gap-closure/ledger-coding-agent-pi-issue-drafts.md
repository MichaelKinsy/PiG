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

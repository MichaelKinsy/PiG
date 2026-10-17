# Bug ledger review: docs/bug-ledger.json against git

Reviewer: lane ledger-coding-agent (lead retask). Scope: the 248 generated entries on integrate-042, compared with their fix commits, guard tests and symptom text. Method: `bugledger-audit.py` (read-only, run from the repo root) plus manual reading of every flagged entry. The generator (`automation/ci/bugledger`) was not changed.

## What checks out
- Every commit hash resolves (726 of 726). Every cited test file and test function exists (0 missing).
- 135 of 136 entries whose symptom names a code identifier have that identifier in at least one fix commit message or diff (the exception is `BF-ed05099-5`, `tool_result`).

## Defects found

### A. Over-broad fix commits (23 entries with 3 or more commits carry commits that never mention the entry's identifiers)
`pickLane` keeps every commit of a small lane (`len(lane) <= 3`) and any commit scoring at least `minRelevance` (4). A generic score lets whole unrelated lanes attach. Worst cases:
- `BF-8c7bacf` (`ApplyImmutable`): 17 of 21 commits are unrelated ssh, faux provider, protocol and chord commits.
- `BF-4610c9f` (`BashExecutionComponent`): 6 of 7 commits are about stdin close, extension cache cleanup, npm publishing, durable harness races and a sprite picker. Its root cause text is an unrelated `TestTaskAbort` paragraph.
- `BF-0b674b3` (Gemini lowercasing and `parseInt`): 5 of 7 commits are other `ai` oracle ports (error-body, json-parse, node-http-proxy, openai-codex, radius-config).
- `BF-4bcc7da` (`StreamProxy`): 4 of 7 are tui editor and paste commits. Its root cause describes bracketed paste, not the proxy.
- Also: `BF-ebb4c0e`, `BF-d276c00`, `BF-f68aa40`, `BF-31cde9f`, `BF-db7007d`, `BF-448d2a9`, `BF-f8673e6`, `BF-504869b`, `BF-34bcd58`, `BF-570f857`, `BF-2718ece`, `BF-8b75dc0`, `BF-165cf58-2`, `BF-6f4a82f`, `BF-c9f1cff`, `BF-e2db931`, `BF-d7bccf4`, `BF-ce0f352`, `BF-D110`.
Consequence: the "fix commit(s)" column and the root cause are wrong for these entries. Suggested generator rule: when a lane has several commits, keep only commits whose message or diff contains one of the note's identifiers (or, with none, the anchor commit); never fall back to the whole lane for a note that names identifiers.

### B. Entries with no real fix or guard
- Only a changelog commit is attached (no production file touched): `BF-c1fedc2`, `BF-c1fedc2-2` (commit "Add the port-104 changelog fragment"), `BF-de91c76`, `BF-de91c76-2`, `BF-f288d69`, `BF-f288d69-3`, `BF-f288d69-4`, `BF-092f582`, `BF-d6e0dca`.
- No guard test: `BF-c1fedc2`, `BF-c1fedc2-2`, `BF-de91c76`, `BF-de91c76-2`, `BF-f288d69`, `BF-f288d69-4`, `BF-092f582`, `BF-4fab2ef`, `BF-ca27ba6`, `BF-7d0621f`. The task says CI fails an entry that points at a missing test; entries with none at all should be listed as unguarded with the reason, or exempted explicitly.

### C. Symptoms that are not self-contained
Seven symptoms start with a pronoun because a note sentence was split from its subject: `BF-4d965e3`, `BF-6708fbb`, `BF-b4822ef`, `BF-8c7bacf` ("It accepted them, and only `ApplyImmutable` rejected them while applying."), `BF-8c7bacf-2`, `BF-6992ffb`, `BF-ca27ba6`. `symptomOf` should keep the preceding sentence.

### D. Root cause states the change, not the cause, or is shared between bugs
- Describes the fix: `BF-d133e3d` ("Regenerate shims/pi-dist ..."), `BF-0e8761b` ("Hidden declarations stay out of the tool list ..."), `BF-a53f72f` ("Renames the Go types ..." for a stale-context bug), `BF-f24a8ee`, `BF-f24a8ee-2` ("Ports ...", the second entry is about text selection but repeats the herdr text), `BF-0bc05bf`.
- One commit message paragraph is reused as root cause for several different bugs: `BF-47c3007` (3 entries), `BF-f288d69` (4), `BF-a4753ec` (3 plus `BF-b5ebeb5`), `BF-9b63b2b`/`BF-165cf58` (4), `BF-ccaade2`/`BF-4d04134`/`BF-082355f`.
- 21 entries have an empty root cause (`BF-affc944`, `BF-affc944-2`, `BF-c1fedc2` ... see the script output). An empty cause is honest; a copied one is not.

## Recommended generator checks (shrink-only, like the other gates)
1. Fail an entry whose symptom names an identifier that appears in none of its fix commits.
2. Fail an entry whose only commits touch no non-test, non-doc file.
3. Fail two entries that share an identical root cause while their symptoms differ.
4. Fail a symptom that starts with a pronoun.
Apply them with an exempt list for the existing entries, then shrink it as the generator improves.

## E. Source classes (second pass)
- `ahead-of-pi` (28 entries): all 28 divergence ids resolve to a `docs/parity/DIVERGENCES.md` section marked `SCRUTINIZED:approved`, and every entry has a remove-when that appears in that section. 5 have an empty `piIssue`: `BF-f288d69-2` (D99), `BF-D110` (D110), `BF-9be5765` (D111), `BF-85152a8` (D115) and `BF-0413ea7` (D116). The task requires "divergence number + Pi issue + removed when Pi fixes it"; for these either file or cite the Pi issue, or state in the entry that none exists. The other 23 carry a `github.com/earendil-works/pi/issues/N` link, taken from the divergence record's own "pi-mono #N" or "Pi #N" title (for example D120-D128 and D150-D156).
- `pi-port` (15 entries): `BF-70d51e5-2` ("Fixed the message-format page ...") is a documentation fix, not a Pi fix ported. `BF-47c3007`, `-2`, `-3` share one root cause and the three are three separate MCP bugs. Class comes from `piPortCommit` and `piPortRE` matching words in the note or commit message; it is not checked against a Pi change, so each entry should name the Pi file or release it follows (most do not: 11 of 15 release notes never use the word Pi).

- Follow-up: the 5 ledger entries without a Pi issue (D99, D110, D111, D115, D116) have no issue to link: a search of Pi's tracker found none, and the nearest hits describe other defects. See `ledger-coding-agent-pi-issue-drafts.md` for the search and five drafts for the owner to file; the generator already prints "none recorded" until a `Pi issue:` line exists in `docs/parity/DIVERGENCES.md`.

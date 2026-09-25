# Historical local rehearsal receipt

This receipt describes the complete local rehearsal. Game footage and full composites are not distributed; the public subset is listed in `../media.json`.

Rehearsal date: 2026-09-25 UTC. The recordings use the production sources at `06d42102a5`. That base contains the Responses tool-identity correction `6e9f198270` and the completed exit-teardown changes (`6a1db63933`, `7478e77c84`, `2b67ebabdd`, and `d9256a6856`). This media refresh changes no production code, public API, default configuration, or parity ledger.

## Recorder regressions and corrections

The previous B and F final frames contain two resume hints, an off-column shell prompt, and `sess-` identities. The refreshed frames contain one hint, a UUIDv7 identity, and a column-zero prompt. The upstream shutdown rule is `.upstream/current/packages/coding-agent/src/modes/interactive/interactive-mode.ts:shutdown`: stop and dispose the interactive runtime before writing the single resume command.

Two existing recording checks fail before the fixture corrections:

- The native Go + Rust Piglet build fails with `E0631` at all three `chain-rs` event handlers. The current SDK requires `Fn(&Context, &mut Value)`, while the fixture takes `Value`. Borrowing the three ignored parameters restores compilation without changing prompts or behavior. The same build succeeds before recording and again on camera in C. F independently builds and runs the isolated Rust extension and checks all four real tool results.
- The quickstart tape times out on `Wait+Screen /Ready\./`. Its failure dump contains the current compact startup header instead. The replacement wait asserts `Press ctrl+o to show full startup help and loaded resources.`, matching the pinned upstream header. The complete tape then passes its answer, live tool, resume, continuation, and extension notification assertions.

All visible commands, explicit sleeps, typing speeds, frame rates, poster timestamps, and hook excerpts remain unchanged. C waits for real compilation, so its shorter build changes the total duration without speeding up the footage. No failed take supplies a delivered clip.

## Final-frame and tool-card review

The review decodes the final second of each delivered MP4, reverses those decoded frames, and extracts the first resulting frame. This selects the actual last frame, not an approximate seek. Separate sampled sequences check live tools, resumed tools, and gameplay. Column zero means the terminal's first column, inside the unchanged VHS padding.

| Clip | MP4 bytes | Duration | Last-frame check |
|---|---:|---:|---|
| `demo-a` | 662,168 | 22.35 s | One Pi resume hint with UUIDv7; passing unittest output; shell prompt at column zero. |
| `demo-b` | 644,473 | 24.10 s | One PiG resume hint with UUIDv7; passing unittest output; shell prompt at column zero. |
| `demo-c` | 260,804 | 43.75 s | Both native builds finish and print hashes; shell prompt at column zero. No interactive session or tool cards. |
| `demo-d` | 165,448 | 16.15 s | Copied binary finishes the faux-provider print request with empty PATH; shell prompt at column zero. No interactive resume hint or tool cards. |
| `demo-e` | 203,044 | 18.50 s | Runner reports score 19 and high score 43; shell prompt at column zero. No model conversation, so no resume hint or tool cards. |
| `demo-f` | 1,148,097 | 38.45 s | One PiG resume hint with UUIDv7; four-tool chain completed; shell prompt at column zero. F remains visibly BLOCKED. |
| `demo-full` | 3,080,555 | 163.30 s | Ends with the same checked F frame: one resume hint and column-zero prompt; BLOCKED label retained. |
| `demo-hook` | 77,213 | 5.00 s | Ends on actual Runner gameplay. An exit hint, shell prompt, and tool card are not applicable to that frame. |
| `quickstart` | 536,820 | 68.760 s | Expected source-update refusal and `exit=1`; final shell prompt at column zero. No interactive exit occurs in this final frame. |

A, B, and F each execute exactly the expected `bash`, `read`, `edit`, `bash` tool sequence. The checked Session results contain no tool errors, and an independent unittest rerun confirms the exact on-disk repair. Sampled tool frames show one card for each call. Quickstart shows one Bash card in the live view and one in the restored view, not duplicate cards in either view. Its local JSON verifier independently checks one execution start, one matching execution end, and the exact `42\n` result. Extension notifications use the fixed status styling.

F runs the Rust chain, copies the local Doom extension, reloads it, and enters the real game. The frame and HUD remain cropped. The recording, poster, full-video segment, and evidence retain the BLOCKED disposition from [DEMO-BLOCKERS.md](../DEMO-BLOCKERS.md). This refresh does not claim complete Node custom-overlay parity.

## Other delivered files

| File | Bytes |
|---|---:|
| `demo-full.gif` | 10,273,152 |
| `demo-a.png` | 166,708 |
| `demo-b.png` | 169,609 |
| `demo-c.png` | 270,438 |
| `demo-d.png` | 305,221 |
| `demo-e.png` | 110,279 |
| `demo-f.png` | 310,514 |
| `demo-full.png` | 166,708 |
| `demo-hook.png` | 109,168 |
| `quickstart.gif` | 1,566,244 |
| `quickstart-poster.png` | 100,140 |

All media is 1280×720. Every MP4 passes its H.264/yuv420p, size, decode, and faststart checks. The hook lasts exactly five seconds. The quickstart GIF lasts 68.760 seconds. Its regenerated poster is pixel-equal to the first decoded MP4 frame and byte-identical to the previous poster. [media.json](../media.json) binds the demo inventory to exact SHA-256 hashes. The quickstart ASCII snapshots come from the delivered take.

## Passing commands

```bash
go build ./...
go vet ./...
go test ./cmd/pig ./tui
go test -race ./cmd/pig ./tui
go tool golangci-lint config verify
make lint-changed LINT_BASE=staging/mirror/main
make lint
python3 -B -m unittest discover -s media/demo/tests -v
vhs validate media/quickstart/quickstart.tape
python3 -B media/quickstart/verify-local.py "$SCRATCH/bin/pig"
python3 -B media/demo/verify-media.py --tools "$TOOLS"
python3 -B media/quickstart/verify-media.py
```

The complete non-closure contract gate also exits zero:

```bash
make -o interface-deps -k \
  correspondence-check porter-check \
  interface-inventory interface-inventory-test \
  interface-go-drift interface-recommendations-drift \
  interface-mapping-quality interface-delta behavior-contracts \
  test-inventory-drift test-inventory format-version-inventory \
  custom-factory-ledger-drift
```

`-o interface-deps` prevents installation; it does not skip inventory or compiler tests. No generated interface inventory needs regeneration because no public Go API changes. The initial unqualified `make lint-changed` uses the stale local `main` denominator and selects nested-module fixtures that the root module cannot type-check. Selecting the task's actual base, `staging/mirror/main`, passes. Full `make lint` also passes. No source changes, suppressions, or baseline increases address that invocation error.

## Isolation and retained evidence

The existing staging, recorder, encoder, and quickstart renderer execute real commands with fresh HOMEs and local faux providers. Recording outputs, staged HOMEs, and owned compiler caches stay in the lane's scratch directory. Scratch copies of the demo launchers change only the allowed staging root, temporary-directory plumbing, and auxiliary ASCII capture. Long temporary paths initially prevent Chrome startup. A short descriptor alias resolves that recorder-only failure for the demo. The final quickstart uses the installed Bubblewrap to bind its scratch temporary directory as `/tmp`, retaining short on-screen paths without writing into the host's `/tmp`. A scratch VHS launcher enables Chrome's local capture-only no-sandbox option. No installed tool, dependency, product default, or checked-in rendering algorithm changes.

The raw takes, recorder failures, exact last-frame PNGs, sampled sequences, compiler diagnostics, and gate logs remain in lane scratch. Providers terminate and are joined after each take. No full Session logs, writable HOMEs, compiler caches, third-party WAD/WASM files, or personal paths are committed. No Earendil service, hosted model, login, public installation, publication, or deployment is part of this refresh. There is no STOP-AND-ASK service question.

The earlier modal-clipping regression artifacts remain historical evidence for `db4023ec0`; they are not rerun or relabelled as a new production fix here. This receipt claims neither a whole-repository `go test ./...` run nor a full paired parity gate.

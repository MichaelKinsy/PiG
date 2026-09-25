# Launch demo blockers

## Current per-step disposition

A, B, C, D, and E pass their actual rehearsal paths. F is **BLOCKED for complete Doom presentation**. Its Rust chain, tool execution, local extension discovery, and `/reload` execute successfully. Its Doom engine renders and accepts input, but the remote overlay crops the frame and HUD. The delivered `demo-f.mp4`, `demo-f.png`, and F segment in the full recording carry an explicit BLOCKED label. They are not proof of complete custom-overlay parity.

## Open: Node remote overlay sizing and modal framing

### Reproduce

Use a fresh HOME from [DEMO-RUNBOOK.md](DEMO-RUNBOOK.md). The WAD and generated WASM must already exist locally. Use the same staged assets for both hosts.

```text
Pi:
  pi -e ~/doom-overlay
  /doom-overlay ../doom-overlay/doom1.wad

PiG:
  pig -e ~/chain-rs
  /chain fix the failing tests
  !cp -R ~/doom-overlay .pig/extensions/doom-overlay
  /reload
  /doom-overlay ./.pig/extensions/doom-overlay/doom1.wad
```

Press Enter to enter the game menus, then movement/fire keys. Press `q` to return to the editor. The pinned Pi path shows the game with its HUD and footer. The PiG path loses the right/bottom of the rendered frame and adds a modal border. The local Pi oracle screenshot and final PiG screenshot are withheld pending game-footage redistribution clearance.

### Source findings

- Upstream `examples/extensions/doom-overlay/index.ts` supplies `overlay: true`, `width: "75%"`, `maxHeight: "95%"`, `anchor: "center"`, and `margin: { top: 1 }`.
- `coding/extension/host/subprocess/runtime-node/runtime.mjs`, `openCustomOverlay`, serializes only the older `widthFraction`/`heightFraction` options. It renders the component at `this.ready.width`, not the resolved inner overlay width.
- `internal/codingagent/ext_ui_context.go`, `RunRemoteOverlay`, defaults those fractions to 0.75 and 0.7. Its `OpenOverlay` path creates a modal wrapper.
- `tui/overlay_compositor.go`, `modalOverlay.renderModal`, clips that full-width cached frame to the smaller modal rectangle.

A faithful fix needs the current upstream overlay option contract, resolved render dimensions, border ownership, resize behavior, and caller tests across the bridge. That is wider than the bounded clipping correction in this lane. The lead owns this remaining blocker. Do not work around it by editing Doom's render width, stripping its footer, forcing fullscreen, or using Pi footage labeled as PiG. No divergence approval is requested or implied by this report.

## Fixed: modal clipping discarded colors and split wide characters

Before the fix, overflowing colored half-block rows became plain horizontal stripes. The same Doom source/assets rendered in color in Pi. The local before-fix screenshot retains the failing PiG image; it is withheld pending game-footage redistribution clearance.

The cause was `tui/tui.go:drawBox`: it stripped ANSI and sliced runes when the visible width exceeded the box. That also split a terminal-cell boundary for wide text. Commit `db4023ec0` uses the existing ANSI/grapheme-aware width helper and recomputes the retained cell width. It changes no public API, overlay sizing policy, extension protocol, or default product behavior.

The upstream rule is visible in `.upstream/current/packages/tui/src/tui.ts:compositeTuiLine`: overlay clipping preserves ANSI state and uses terminal cells. The source-level fix applies to every caller of the modal box, not only Doom.

Failing-first tests:

- `TestDrawBoxClipsColoredCellsWithoutDiscardingSGR`: empty, fitting, overflowing truecolor, and wide-character boundary cases.
- `TestModalOverlayClipsRemoteFrameWithoutLosingColors`: modal composition in regular and fullscreen renderers.

Both tests compile and fail against the base source. [evidence/modal-red.txt](evidence/modal-red.txt) contains the exact failures. `go test ./tui` and `go test -race ./tui` pass after the correction. The actual Node/Doom path renders colors after the fix. Geometry remains open as described above.

### Resource evidence

`BenchmarkModalColoredClipping` exercises a 120-column, 32-row colored remote frame clipped into a modal. It does not copy Session history or add tasks, processes, timers, or retained caches. Input size bounds the additional clipping work. The existing overlay lifecycle still owns the frame and closes its producer.

The observed samples change from approximately 0.86–0.90 ms, 297 KB, and 178 allocations per composition to 2.01–2.40 ms, 956 KB, and roughly 2,726 allocations. Preserving ANSI costs more than discarding it; this is **not** a performance improvement claim. The worst sampled composition is below the existing 16 ms frame interval on this host, not a portable latency guarantee. The CPU profile identifies ANSI scanning and GC work. The allocation profile identifies string building and width truncation. Raw profiles stay in lane scratch; the benchmark outputs are retained in `evidence/modal-benchmark-*.txt`.

Reproduce the benchmark and profiles:

```bash
go test ./tui -run '^$' -bench '^BenchmarkModalColoredClipping$' -benchmem -count=3 \
  -cpuprofile "$SCRATCH/modal-cpu.pprof" -memprofile "$SCRATCH/modal-mem.pprof" \
  -o "$SCRATCH/tui.test"
go tool pprof -top "$SCRATCH/modal-cpu.pprof"
go tool pprof -top -alloc_space "$SCRATCH/modal-mem.pprof"
```

## Fixed: staged rehearsal scripts and faux-provider claims

The staged launchers refer to Mac-only paths, source a Mac development wrapper, and attempt remote builds and package acquisition. The local copies now resolve installed tools, build this checkout, create fresh `/tmp` HOMEs, and use no installer or remote shell. Read-only upstream directory modes initially prevented staging the WAD; only the disposable copy is made writable.

The staged model narrated success even after a failing tool and claimed a universal single-file/no-install guarantee. The model now identifies known failed tool responses and labels its greeting as scripted. Its unittest command no longer pipes through `tail`, which hid the actual exit status. A first attempt at error detection also mistook the source text `raise ValueError(...)` for a failed tool; a separate regression now protects that case.

`python3 -B -m unittest discover -s media/demo/tests -v` checks these fixture contracts. The original staged copies fail the error-propagation, packaging-claim, exit-status, and portable-launcher guards before correction. The real recordings additionally verify actual tool results and the repaired file; scripted text alone is never acceptance evidence.

## Tooling and gate issues resolved during rehearsal

- No installations or downloads occur. The requested static ttyd/ffmpeg/ffprobe and VHS already exist in the shared tools directory. Chrome already exists on the host.
- VHS rejects an arbitrary `Set Shell ./demo-term.sh`. The tapes start supported Bash and enter the isolated demo shell during hidden setup.
- VHS `Wait+Screen` reads old scrollback rows in the installed implementation. Real sessions prove that the chain finished while that query timed out. Recording pauses are bounded, and session/tool/filesystem checks remain mandatory. This is not a PiG parity comparator change.
- An isolated gate PATH initially omitted npm. Symlinking its launcher also failed because that launcher resolves files relative to its own directory. Running the existing npm from its installed directory fixes the gate. No npm installation occurs.
- The first combined test command's 240-second harness budget expired during the race run after `go test ./cmd/pig` passed. A separate `go test -race ./cmd/pig` completes successfully with a sufficient command budget. No test is skipped or assertion weakened.

## Verification scope

The lane runs build, vet, touched-package tests and race tests, lint, all `check-contracts-fast` targets except `closure-check`, fixture tests, actual VHS takes, and media validation. The installed TypeScript dependency is used with `make -o interface-deps` so its installation recipe cannot run. The contract tests still run; no generated inventory or ratchet is raised.

This lane does not claim a complete `go test ./...` run, a full paired parity gate, new PORT_MAP coverage, or complete Node custom-overlay parity. The focused renderer tests and real Pi/PiG probes are the source-fix evidence. The complete contract and release verifier remains the lead's responsibility.

No operation contacts an Earendil service or changes the default Pi-compatible configuration. There is no STOP-AND-ASK service question. Public publication/deployment is not authorized. A public-release owner still needs to approve the recording, resolve F or omit it with disclosure, and review third-party game footage rights.

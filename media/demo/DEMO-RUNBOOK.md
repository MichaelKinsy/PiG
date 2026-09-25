# Launch demo runbook

This is a **local rehearsal with a scripted faux provider**, not a live-model performance demonstration or a release announcement. The terminal commands, tool calls, file edits, builds, copied binary, and game frames execute for real. Every delivered clip keeps a rehearsal label on screen. Step F remains visibly **BLOCKED** for overlay geometry. See [DEMO-BLOCKERS.md](DEMO-BLOCKERS.md).

PiG is a Go implementation of [Pi](https://github.com/earendil-works/pi), the reference implementation. Read [Pi's documentation](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/README.md) for its extension API. Michael Kinsy created PiG. PiG was originally developed at Hewlett Packard Enterprise. This rehearsal does not imply Pi-maintainer endorsement, HPE sponsorship, Brand approval, or authorization to publish.

## Deliverables and observed results

The final takes use PiG production sources at `06d42102a5`, including the Responses tool-identity and exit-teardown fixes. The media fixtures use the current Rust SDK event signature and compact startup header. The Pi version comes from `coding/pigversion/pigversion.go`. The observed Pi comparator reports the same pinned version. The `sources/chain/index.ts` bytes are identical in Pi and PiG and unchanged from the staged `pkgs/chain` fixture. This is the four-step demo example, **not** the different npm `pi-chain` package that seeds a new session.

| Step | Actual path | Result | Clip duration | MP4 bytes |
|---|---|---|---:|---:|
| A | Pi + TypeScript `/chain` | PASS: scout → plan → edit → real unittest | 22.35 s | 662,168 |
| B | PiG `-e` + identical TypeScript `/chain` | PASS: same four stages and repaired file | 24.10 s | 644,473 |
| C | Native Linux Piglet builds: Go; Go + Rust | PASS: fused Go Runner; embedded Rust subprocess | 43.75 s | 260,804 |
| D | Copy the Go + Rust binary to a clean HOME | PASS: faux request with `PATH=/nonexistent` | 16.15 s | 165,448 |
| E | PiG Runner in the built binary | PASS: real start, jump, collision, retry, quit, saved score | 18.50 s | 203,044 |
| F | Rust `/chain`, local extension copy, `/reload`, Doom | BLOCKED: chain/reload work; Doom renders but frame/HUD are cropped | 38.45 s | 1,148,097 |

`demo-full.mp4` concatenates A–F without speed changes. It lasts 163.30 seconds and occupies 3,080,555 bytes. `demo-full.gif` contains the same sequence at six frames per second. `demo-hook.mp4` lasts exactly five seconds. It joins 2.5 seconds of the Pi chain result with 2.5 seconds of a real Runner jump. It does not imply that Doom passed.

Every MP4 is 1280×720, H.264, yuv420p, faststart, and below 10,000,000 bytes. There is no audio track. The eight PNG posters also measure 1280×720. [media.json](media.json) records the exact sizes, hashes, durations, and codecs. The GIF is approximately 10.27 MB; the size limit applies to MP4s. [The verification receipt](evidence/verification.md) records the last-frame checks.

## Prerequisites: use installed tools only

Do not install or download anything for this runbook. Do not run `npm pack`, `npm install`, `npm ci`, `go install`, dependency bootstrap targets, or VHS publication commands. Do not write into `node_modules`.

The rehearsal uses existing Go, Node, Pi, Rust/Cargo, Python, ripgrep, fd, VHS, ttyd, ffmpeg, ffprobe, and Chrome. It resolves tool locations before changing HOME. It uses installed Go modules and a scratch copy of the Cargo registry with network dependency resolution disabled.

Observed tools: Go 1.27.1, Node 24.19.0, Cargo/Rust 1.97.1, Python 3.12.3, VHS 0.12.1, and ffmpeg/ffprobe 7.0.2. ttyd, ffmpeg, and ffprobe are static Linux executables. These are observations, not additional version pins. Use `go.mod`, `.node-version`, and the upstream pin as the project authorities.

If a tool is absent, record the affected step as BLOCKED. Pi or Node absence blocks A; Node absence also blocks B and the Doom part of F. Native Go/Rust steps do not establish interpreted-language portability. Do not replace a failed operation with prerecorded success text.

## Prepare from the repository root

Use the staged local Doom assets. The WAD and generated WASM are inputs, not files distributed in this branch. The upstream TypeScript Doom extension is copied from `.upstream/current` without edits. The WASM executes inside the ordinary Node extension process; this adds no WASM runtime to Stock PiG.

```bash
REPO=$PWD
SCRATCH=${DEMO_SCRATCH:-"$HOME/demo-work/scratch"}
TOOLS=${DEMO_TOOLS:-"$HOME/demo-work/tools/bin"}
ASSETS=${DEMO_ASSETS:-"$HOME/demo-work/assets/doom-overlay"}
mkdir -p "$SCRATCH/bin" "$SCRATCH/takes"
CGO_ENABLED=0 go build -buildvcs=false -trimpath \
  -ldflags "-s -w -X main.Build=$(git rev-parse --short HEAD)" \
  -o "$SCRATCH/bin/pig" ./cmd/pig
ROOT=$(media/demo/stage.sh --pig "$SCRATCH/bin/pig" \
  --assets "$ASSETS" --cache "$SCRATCH/cache")
printf '%s\n' "$ROOT" > "$SCRATCH/root.txt"
```

`stage.sh` creates a new `/tmp/pig-launch-*` directory with mode 0700. It never replaces an existing HOME. It configures both `.pi` and `.pig` for the loopback provider. It copies the intentionally broken `evals/tasks/fix-off-by-one` fixture. It checks that the two last-window tests fail before recording. It configures the original pink Runner sprite explicitly. It does not activate the HPE-named default login artwork.

Keep the shell that holds `ROOT`, `SCRATCH`, and `TOOLS`. The generated `env.sh` and `env.json` contain machine paths for local execution. Do not put those files, full session logs, or compiler caches in a commit or on screen.

The default model port is 17878 on `127.0.0.1`. Choose another unprivileged port with `stage.sh --port` if it is occupied. The recorder starts, health-checks, and terminates its own provider for each take. It rejects provider startup failure. `PI_OFFLINE=1`, `PIG_OFFLINE=1`, and `PI_TELEMETRY=0` prevent update/catalog/telemetry activity during rehearsal. Only the synthetic key `demo-no-key` exists in the model configuration.

## Record all steps

Run the takes serially. C supplies the binary for D and E. F activates a workspace extension and therefore runs last. Use a fresh staged HOME for a second complete recording; do not delete or reset user data to reuse one.

```bash
for step in a b c d e f; do
  python3 -B media/demo/record.py --root "$ROOT" \
    --scratch "$SCRATCH/takes" --tools "$TOOLS" --step "$step" || break
done
```

The recorder launches VHS with the staged environment. `demo-term.sh` enters another `env -i` boundary before Bash starts. Neither the recorder nor the demo shell inherits credentials, user startup scripts, real agent settings, or session history. Chrome uses a disposable profile. VHS's no-sandbox option is limited to this local rendering process, not a PiG default or a sandbox claim about the coding agent.

Each take has its own VHS log, faux-provider log, raw MP4, and checked evidence JSON in scratch. The evidence checks the four ordered user turns, the actual bash/read/edit/bash tool results, the absence of tool errors, the final unittest output, the exact repaired file, and an independent unittest rerun for A, B, and F. D checks the copied binary's bytes and a completed request in the clean HOME. E checks a persisted nonzero Runner score. Review its frames as well; a score alone is not visual proof.

### On-screen commands and pauses

The exact keystrokes and timings live in `tapes/a.tape` through `tapes/f.tape`. The principal commands are:

```text
A: pi --version
   pi -e ~/chain
   /chain fix the failing tests
   Ctrl+D
   python3 -m unittest -v test_stats

B: pig --version
   pig -e ~/chain
   /chain fix the failing tests
   Ctrl+D
   python3 -m unittest -v test_stats

C: build-piglets.sh
   # The script executes both commands, retains diagnostics, and prints elapsed wall time:
   pig piglet build pig-go.yaml --format binary --out ../dist/pig-go
   pig piglet build pig-demo.yaml --format binary --out ../dist/pig-demo

D: remote-stage.sh
   cp ~/dist/pig-demo ~/clean/pig-demo
   cd ~/clean
   env -i HOME=$PWD PATH=/nonexistent PIG_OFFLINE=1 PI_OFFLINE=1 ./pig-demo -p hello

E: ~/dist/pig-demo
   /runner
   Space, timed jumps, r, Space, q, Ctrl+D

F: pig -e ~/chain-rs
   /chain fix the failing tests
   !cp -R ~/doom-overlay .pig/extensions/doom-overlay
   /reload
   /doom-overlay ./.pig/extensions/doom-overlay/doom1.wad
   Enter, movement/fire input, q, Ctrl+D
```

`remote-stage.sh` retains its staged name but now prepares a second **local** HOME. D is a same-host Linux handoff, not a remote-host, container, cross-OS, or ABI-portability claim. The binary contains the Go and Rust executable components; the loopback service and model configuration remain external.

Recorded warm-cache C builds take 3 seconds for Go and 34 seconds for Go + Rust. These wall times are observations on this host, not performance comparisons. The scripted chain takes approximately 4.4–4.6 seconds with artificial streaming pauses. VHS encoding overhead is not part of those application timings. The commands, explicit pauses, frame rates, and excerpt timings are unchanged; C waits for real build completion, so its duration varies with build time.

The tapes allow startup pauses of 2–3 seconds and chain pauses of 9–11 seconds. C waits for the real shell prompt with a 180-second bound. `record.py` has a 240-second VHS deadline. If the application does not finish within a take, its post-recording checks fail; increase the recording pause or record the actual failure, never fabricate completion.

VHS `Wait+Screen` reads rows from the beginning of scrollback in the installed version. It can time out while the visible chain has already finished. The tapes use bounded capture pauses plus independent session/tool/filesystem checks rather than treating that broken screen query as a product failure. No parity comparator is weakened.

## Encode and validate

Review the raw game footage before encoding. Keep F marked BLOCKED until its actual geometry matches Pi. Do not crop away the faulty Doom output to make it look correct.

```bash
python3 -B media/demo/encode.py --scratch "$SCRATCH/takes" --tools "$TOOLS"
python3 -B media/demo/verify-media.py --tools "$TOOLS"
python3 -B -m unittest discover -s media/demo/tests -v
```

`encode.py` verifies raw-take hashes against the recorder's evidence. It scales the entire terminal image into a labeled 1280×720 frame. It does not substitute terminal output, remove failures, accelerate builds, or change model/tool content. It writes all outputs under `media/demo/`. It adds the explicit F blocker label. It creates posters from the actual encoded frames.

`verify-media.py` probes every required file, checks dimensions and codec, decodes every file, checks each MP4 size, and verifies that `moov` precedes `mdat`. It rejects a hook that is not exactly five seconds. `encode.py` owns regeneration of `media.json`; do not hand-edit its hashes.

## Cleanup and fallback

The recorder terminates and joins its faux provider in a `finally` block. VHS closes its terminal and browser at the end of each take. No demo processes remain after the final run. Scratch and isolated HOME files remain for diagnosis. Remove only the exact directory returned by your own `stage.sh` invocation after reviewing the evidence. Never operate on the real `.pig` or `.pi` tree, kill a tmux server, or reset a user's repository.

If C fails, inspect `"$ROOT/logs/build-pig-go.log"` or `"$ROOT/logs/build-pig-demo.log"`. Do not label D/E successful using an unrelated binary. If Doom assets are missing, do not allow its WAD downloader to substitute network content; mark the Doom portion BLOCKED. This recording passes the local WAD path explicitly.

No publication, deployment, hosted model request, public upload, HPE-branded login, or Earendil-operated service is part of these commands. Public distribution and game-footage rights review remain separate owner decisions.

## Source and asset provenance

- `sources/chain`: unchanged staged `pkgs/chain`; MIT metadata retained. `index.ts` SHA-256: `53841e011b893b491a1652b2ca5f4db8b051449bb39a41b22427bc6e338c00e0`.
- `sources/chain-rs`: staged Rust chain factory with its three ignored event-data parameters borrowed as `&mut Value` to match the current SDK. Prompts and behavior are unchanged. Pig stages the matching SDK at build time.
- Runner: this repository's `piglets/standard` Resources, selected explicitly by generated demo Piglets. No Stock PiG behavior is activated by default.
- Doom overlay TypeScript: exact `.upstream/current/packages/coding-agent/examples/extensions/doom-overlay` source. Credits: [id Software](https://github.com/id-Software/DOOM), [doomgeneric](https://github.com/ozkl/doomgeneric), and [pi-doom](https://github.com/badlogic/pi-doom).
- Local `doom1.wad` SHA-256: `1d7d43be501e67d927e415e0b8f3e29c3bf33075e859721816f652a526cac771`.
- Local `doom.wasm` SHA-256: `571d161956593508cf4ade732ae93753f00484bb526667a8676571cca14dec7d`.
- Local `doom.js` SHA-256: `c3147cf4881c88b4210c017c26828f52aff83f0c4db27d7dad3fe6cda9ade1fb`.

The repository distributes clips A–E and the hook plus source recipes. It does not distribute the game footage, third-party WAD/WASM binaries, or a Piglet release.

## Public media subset

Only clips A–E and the hook, with their posters, ship in this repository. Step F, the full composites, and Doom screenshots are excluded pending game-footage redistribution clearance. The full rehearsal commands above can produce those assets locally; generating them is not publication approval. `verify-media.py` checks the public subset by default; `--rehearsal` retains the full original validation.

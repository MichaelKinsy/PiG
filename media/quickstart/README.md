<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Quickstart recording and evidence

This is a **local rehearsal against an unpublished PiG source build**, not proof of a public installation. The faux provider returns scripted responses without credentials. The Bash tool, Session persistence, `/resume`, and generated Go extension run through PiG's real production paths. No product Piglet, remote package, login, curl shim, Git shim, or alternate release server is used.

See [`QUICKSTART.md`](../../QUICKSTART.md) for the commands and captured output. Pi is the [reference implementation](https://github.com/earendil-works/pi), with its own [documentation](https://pi.dev/docs/latest). Michael Kinsy created PiG, originally developed at Hewlett Packard Enterprise. This recording implies no Pi-maintainer endorsement or public-release approval.

## Reproduce with already-installed tools

Use a Linux source checkout with its dependencies already cached. Put existing native executables on PATH, not auto-installing version-manager shims. The recording needs Go, VHS, ttyd, ffmpeg, ffprobe, Chrome, Bash, Python 3, ripgrep, fd, and DejaVu Sans Mono. Missing prerequisites are errors; the scripts never install them. The local command verifier needs only the existing PiG binary, Go, Python, Bash, and cached dependencies.

Build and verify the credential-free command path from the repository root:

```bash
go build -o /tmp/qs-bin/pig ./cmd/pig
python3 media/quickstart/verify-local.py /tmp/qs-bin/pig
```

Render the tape and verify the resulting files:

```bash
bash media/quickstart/render.sh
python3 media/quickstart/verify-media.py
```

`render.sh` creates a fresh temporary HOME and an empty `~/demo` directory. It clears inherited credentials, PiG overrides, shell history, and user startup files. It disables package/catalog update checks and Go downloads. It resolves the existing Go toolchain before changing HOME. The build and extension compiler reuse the existing Go caches; the scripts do not modify source dependencies or install tools. Use a short temporary root outside your real HOME for a recording with no personal paths on screen.

The tape builds PiG from the current checkout into the disposable HOME on camera. It shows the composite version emitted by that binary. The pin and Go requirements come from the source checkout, not from tape constants. The visible version in the committed recording is an observation, not a public-release claim.

The renderer prints the temporary evidence directory before it starts. That directory retains the VHS log, ASCII screen snapshots, raw MP4, generated extension, and saved sessions for inspection. [`quickstart.ascii`](quickstart.ascii) retains the committed run's raw VHS snapshots. Fullscreen snapshots show the actual TUI; shell snapshots can include stale scrollback because of the VHS limitation below. Use the video and local command verifier for shell output, not those stale snapshots. VHS owns the browser and ttyd lifetime. The tape exits both PiG processes with Ctrl+D. The renderer does not upload or publish anything.

## What the checks prove

- `verify-local.py` requires the question's exact `42\n` response.
- It requires exactly one Bash execution for the one-tool prompt, with `command=expr 20 + 22`, a matching tool-call ID, and the exact successful tool result.
- It requires the generated `hello_ping` tool in the active model tool schemas after explicit `-e` loading.
- It requires source-update refusal with exit 1 and an unchanged executable digest.
- The tape waits for the current compact startup help, the `/resume` selector, the resume confirmation, the restored Bash command/result, and the continuation response.
- The tape invokes `/hello` and waits for its real notification, not only the extension's registration.
- `verify-media.py` checks duration, dimensions, codec, pixel format, byte ceilings, MP4 faststart box order, and equality of the poster to the first decoded video frame.

These are tutorial acceptance checks, not additional Pi parity-coverage claims. The existing parity ledgers remain authoritative.

## Recording choices

The recording is silent, 1280×720, with a 20-pixel DejaVu Sans Mono font. VHS captures 15 frames per second. ffmpeg encodes H.264/yuv420p with faststart and derives an 8-frame-per-second GIF using a 64-color palette without dithering. The GIF retains the full resolution and duration. The poster is extracted from the delivered MP4, not composed separately.

Fullscreen mode is explicitly selected with `--tui-mode fullscreen`; Stock PiG's default remains regular mode. The installed VHS screen reader reads the first terminal-buffer rows without applying the scrollback offset. In regular mode, a wait for `Took …s` timed out even though the live visible terminal already showed the successful tool result. Fullscreen uses the alternate buffer and makes those assertions observe the displayed screen. No PiG renderer changes or output normalization hide this recorder limitation.

A restored tool result has no live elapsed-time line. The tape therefore asserts the restored command and exact `42` result after the resume confirmation, rather than waiting for a duration that is not in Session history. The initial live tool call still waits for its completion timing line.

## Verified limitations and failed probes

| Reproduction | Observed result | Disposition |
|---|---|---|
| Public `curl -fsSL https://pi-in-go.dev/install.sh \| sh` | The script downloads, but release discovery ends in HTTP 404 after transient 502 responses; installer exits 1. | No successful public install is claimed. Exact output is in the guide. |
| Public `go install github.com/MichaelKinsy/PiG/cmd/pig@latest` with a fresh HOME and Git terminal prompting disabled | GitHub requires authentication; exits 1. | Public module installation is unavailable in this verification. |
| Source-built `pig update` | Refuses unknown installation ownership; exits 1 without changing the binary. | Expected D39 behavior, not a product bug or update success. |
| VHS wait for a literal trailing space after `$` | VHS trims trailing spaces; the initial shell wait times out. | The tape matches the prompt with optional trailing whitespace. |
| VHS regular-screen wait after the transcript scrolls | The visible tool completes, but VHS reads stale buffer rows and times out. | Explicit fullscreen recording; no change to PiG defaults. |
| Waiting for `Took …s` after `/resume` | The restored command and result appear without a live timing line. | Assert the persisted command/result, not non-persisted timing. |

The public installation probes are historical evidence, not part of the reproduction scripts. The scripts use existing tools and cached dependencies only. They do not install software or exercise a login flow.

Successful standalone-download installation and self-update are not yet verified against a published release. The guide does not claim either succeeds. The local rehearsal does not contact Earendil services or change PiG's defaults.

## Delivered files

| File | Bytes | Verified properties |
|---|---:|---|
| [`quickstart.mp4`](quickstart.mp4) | 536,820 | 68.760 s; 1280×720; H.264; yuv420p; 25 fps output; faststart |
| [`quickstart.gif`](quickstart.gif) | 1,566,244 | 68.760 s; 1280×720; looping; under 6 MB |
| [`quickstart-poster.png`](quickstart-poster.png) | 100,140 | 1280×720; pixel-equal to the MP4's first decoded frame |

This recording uses the production sources at `06d42102a5`, including the Responses tool-identity and exit-teardown fixes. The former `Ready.` tape wait times out on that source. The tape now waits for the actual compact startup help without changing its commands or pauses. The single Bash card appears once live and once in the resumed view, not duplicated within either view. The final frame shows the expected source-update refusal, `exit=1`, and a shell prompt at column zero. The regenerated first-frame poster is byte-identical to the previous poster.

The media guard first failed because the outputs did not exist. After rendering, it passes. Remuxing the valid MP4 without faststart produces a decodable mutation that the guard rejects with `MP4 is not faststart`. The committed files also received a sampled-frame visual review, including the initial labels, tool result, resume selector, extension notification, and update refusal.

## Tool observations

| Tool | Observed version |
|---|---|
| Go | `go1.27.1 linux/amd64` (the checkout's toolchain) |
| VHS | `v0.12.1` |
| ttyd | `1.7.7-40e79c7`, statically linked x86-64 |
| ffmpeg / ffprobe | `7.0.2-static`, johnvansickle.com build |
| Chrome | `151.0.7922.169` |

These are evidence snapshots, not instructions to fetch or replace any tool.

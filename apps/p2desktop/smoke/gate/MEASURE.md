# Long-session measurement, native vs web

Fixture: `go test -run TestMakeFixture ./shell/mygoshell/native` with `P2_FIXTURE_DIR=<dir> P2_FIXTURE_EXCHANGES=5000` (no model: the entries of one
real exchange, read back through `SessionTransfer.history`, repeated with the user's text numbered and appended by `SessionTransfer.seed`, the way a
fork seeds its copy; seconds, not the 55-minute Chat.send fill). Run: `smoke/gate/measure.sh <fixture> 5000 <out>` (native half = `TestMeasureLong`,
MyGo's software tester at 1280x812, NOT a window or GPU; web half = Chrome for Testing + `measure.mjs`, same server and session). Both halves now report
`oldestVisible` (the oldest numbered message on screen after the 400 wheel steps) so a scroll that went nowhere shows.

## Corrections to earlier figures
The 300-exchange native scroll figures of round 4 did not measure scrolling: the tester's wheel glides only as the clock advances (`Tester.Advance`), and
the sign was wrong, so the list never left the end. Fixed in `measure_test.go` (`Wheel(-400)` + `Advance(150ms)` outside the timed frame).

## 5000 exchanges (10k messages), native half, Mac M4 Pro under load 40+ from other agents (noisy)
| | native (software tester) |
|---|---|
| scroll 400 steps: frame mean / p95 / max | 0.38 / 1.6 / 8.42 ms (reached message 3976 from 4999) |
| open to newest message | 6143 ms in this loaded run (97-447 ms in quieter runs, which scrolled nowhere) |
| memory | heap 35 MB, process RSS 188 MB (server + worker + UI) |
| stream frame mean / p95 | 1.9 / 7.23 ms |

## 5000 exchanges (10k messages), native against web, same session (round 6, xd670, Linux x86-64, container with 16 CPUs, host load 40-55)
Fixture: generated on xd670 by `TestMakeFixture` (5.5 MB data directory, `SessionTransfer.seed`, Pi's session format through the Durable store), copied
once and used by one server for both halves. Native half = MyGo's software tester at 1280x812 (`TestMeasureLong`, built in the Go container, run with
`NATIVE_TEST=<binary> smoke/gate/measure.sh`); web half = Chrome headless shell (playwright chromium-1254, software rendering, no GPU) + `measure.mjs`.
The container needs Chrome's libraries (`playwright install-deps`) AND `libpangocairo-1.0-0 fontconfig fonts-dejavu-core fonts-liberation`: without
them MyGo logs "text will not show", still lays out and passes `HasText`, but shapes and draws no glyphs. A first run in that state (scroll 0.56 ms mean) is
void and is not reported; the figures below are with text drawn.

| | native (software tester, text drawn) | web (Chrome headless shell) |
|---|---|---|
| open to newest message | 78 ms | 315 ms |
| 400 wheel steps, total | 1403 ms | 22 165 ms |
| scroll frame mean / p95 / max | 0.95 / 4.85 / 10.6 ms | 55.4 / 67.0 / 86.2 ms |
| oldest message on screen after the 400 steps (of 4999) | 2900 | 4309 |
| stream frame mean / p95 / max | 11.5 / 57.8 / 57.8 ms (16 frames) | 33.4 / 33.8 / 33.8 ms (6 frames) |
| memory | heap 25 MB, process RSS 116 MB (server + worker + UI) | browser RSS 110 MB (sum of the processes whose command line holds the profile directory: renderers started without it are NOT counted, so it undercounts; the server is a separate process) |

Both halves scrolled back a long way; they do not cover the same distance: the native wheel moved 2100 messages in 400 steps, the web 690, so per
STEP the native frame is about 60x cheaper, per message passed about 20x. Caveats: the native half is a software tester, not a window with a GPU, and
times build + layout + paint of a frame, not the display's presentation; the web half is measured from the wheel event to the second
`requestAnimationFrame`, so it includes Chrome's own frame pacing (a floor near 16 ms; 55 ms is 3-4 frames of real work per step at 10k messages).
The native stream p95 is one slow frame of 16 (the first answer's markdown). The stream rows have few frames (a short faux answer): treat the ratio as
direction, not as a benchmark; a window on a GPU is the real-window rerun for a quiet Mac (locked screen now, load 100+).

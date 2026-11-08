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

## 5000 exchanges (10k messages), native against web, same session, valid run (round 6, xd670, Linux x86-64, container with 16 CPUs, host load 40-55)
Fixture: generated on xd670 by `TestMakeFixture` (5.5 MB data directory, `SessionTransfer.seed`, Pi's session format through the Durable store), copied
once and used by one server for both halves. Native half = MyGo's software tester at 1280x812 (`TestMeasureLong`, built in the Go container, run with
`NATIVE_TEST=<binary> smoke/gate/measure.sh`); web half = Chrome headless shell (playwright chromium-1254, software rendering, no GPU) + `measure.mjs`.
Chrome's shared libraries were installed as root (`playwright install-deps`), which also made MyGo's text work on Linux, so the native half now runs
there. Both halves scrolled back through the history (oldest message on screen: native 4175, web 4309, of 4999).

| | native (software tester) | web (Chrome headless shell) |
|---|---|---|
| open to newest message | 71 ms | 414 ms |
| 400 wheel steps, total | 895 ms | 22 304 ms |
| scroll frame mean / p95 / max | 0.56 / 2.03 / 11.1 ms | 55.8 / 67.6 / 117 ms |
| stream frame mean / p95 / max | 5.26 / 14.3 / 21.8 ms (27 frames) | 33.7 / 36.9 / 36.9 ms (6 frames) |
| memory | heap 18 MB, process RSS 94 MB (server + worker + UI) | browser RSS 110 MB (the measured browser process; the server is the native half's process) |

Caveats: the native half is a software tester, not a window with a GPU, and times the build + layout + paint of a frame, not the display's presentation;
the web half is measured from the wheel event to the second `requestAnimationFrame`, so it includes Chrome's own frame pacing (about one 60 Hz frame
per step: its floor is near 16 ms, and the 55 ms mean is 3-4 frames of real work per step at 10k messages). The stream rows have few frames
(a short faux answer). Treat the ratio as direction (native is an order of magnitude cheaper per frame at 10k messages), not as a benchmark; a
window on a GPU is the real-window rerun for a quiet Mac (locked screen now, load 100+).

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

Web half at 5000 exchanges: not obtained validly (the run with the corrected harness was cut by the 900 s timeout on the overloaded Mac, and the
earlier web figures, 12.7 s for 400 steps, mean 31.7 / p95 42.5 ms, browser RSS 1029 MB, came from a run whose native half did not scroll; the web
half itself did scroll, wheel in the page). Rerun when the Mac is quiet: `smoke/gate/measure.sh /tmp/pd5-fix5k 5000 /tmp/pd5-m5k` (regenerate the fixture
with the command above). The native half cannot run on Linux: MyGo needs libgobject for text there, so `HasText` never matches.

# PiG / phi same-machine comparison harness

Measures PiG `main` and [phi](https://github.com/pulseaiclub/phi) side by side on one Linux host: time to first frame, idle resident and proportional memory, binary size, and the allocations of one provider streaming frame. It exists because phi's README numbers (macOS, first-frame method unstated) and PiG's own gates (Linux, different method) cannot be compared directly. No phi code lives in this repository; the phi checkout is cloned separately and only built and run.

Results of a run are written up outside the repository (the 0.5.2 run: `handoffs/0.5.2/phi-vs-pig-measured.md`).

## Subjects and what never happens

| subject | binary | how it is kept off the network |
|---|---|---|
| `pig` | `go build -buildvcs=false -trimpath -ldflags '-s -w -X .../coding/cli.Build=<sha>' ./cmd/pig`, `CGO_ENABLED=0` (the Makefile `install` flags) | `--offline --model test-faux/faux-1` with `PIG_TEST_FAUX=1`: the built-in faux provider, no credentials, no catalog request |
| `phi` | `CGO_ENABLED=0 go build -ldflags='-s -w' ./cmd` (phi's Makefile flags) | `~/.phi/config.yaml` with one model whose `base_url` is `http://127.0.0.1:9/v1` (nothing listens there); `PHI_OFFLINE=1` skips its startup release check |
| `floor` | a 12-line static Go program (`floor/main.go`) that writes one byte | harness floor: fork, exec, pty read and Python overhead, to subtract from both |
| `pig-trace` | `pig` with `PIG_STARTUP_TRACE=1` (stderr to a file, not the pty) | phase split from pig's own marks |
| `pig-metrics` | `pig` with `PIG_PROFILE=metrics`, exited with Ctrl+D | Go heap classes at exit (`process_metrics` in the record) |
| `pig-scratch`, `pig-scratch-nohljs`, `phi-scratch` | scratch builds with `scratch/harness_memclasses.go.txt` (SIGUSR2 dumps `runtime/metrics` memory classes, SIGUSR1 runs `debug.FreeOSMemory` for `memory --freeos`); the pig pair also has `scratch/no-hljs-load.patch`, which makes `tui.LoadAllHighlightLanguages` return at once when `HARNESS_NO_HLJS_LOAD` is set | `pig-scratch` (switch unset) is the control for `pig-scratch-nohljs`; never shipped |
| `phi-fresh` | `phi` as a first install behaves: release check on, `fd`/`rg` missing | informational only: this is the one subject that reaches github.com |

phi downloads `fd` and `rg` from github.com in a goroutine at every TUI start when they are in neither `~/.phi/bin` nor `PATH`, and pig announces them as missing. Every other subject therefore runs with no-op `fd` and `rg` stubs on `PATH`, so neither program downloads anything. `strace -f -e trace=connect` on `pig` and `phi` (method above) shows no `connect` from either; phi still runs one `git branch --show-current` child. phi without the stubs and without `PHI_OFFLINE` connects to github.com:443 (DNS, then the release API and the tool downloads).

Both programs run with the host's default Go settings (`GOMAXPROCS` is whatever the program picks: pig caps its own width, phi uses every CPU in the affinity mask), a private `HOME` and an empty working directory per run, `TERM=xterm-256color`, a 120x40 terminal.

## Definitions

The probe forks the child on a fresh pty with `os.forkpty`, taking `t0` just before the fork. It reads the master side, stamping every chunk with `perf_counter_ns` before looking at it, and answers terminal queries at once. It is a terminal that answers **DA1** (`CSI c` -> `CSI ? 62 ; 22 c`) and **OSC 11** (`rgb:1c1c/1c1c/1c1c`) and nothing else: OSC 10, the OSC 4 palette, XTVERSION, the kitty keyboard and graphics queries, DECRQM and the other queries each program sends go unanswered, as on a terminal that ignores them (DA1 comes last in order, so DA1 closes the wait). Recorded chunks are parsed after the run window closes, so parsing never delays a read.

- **first byte**: the first output chunk after spawn, any content.
- **first non-query output**: the first chunk containing a byte that is not part of a recognised query sequence (the parser in `vtscreen.py` classifies DA1/DA2, XTVERSION, kitty keyboard/graphics queries, DECRQM, XTSMGRAPHICS, CPR/status, window-size queries and `OSC ... ; ?`). Mode sets such as alternate-screen entry count as output, so this is when the program first starts drawing.
- **first glyph**: the first chunk after which the emulated screen holds at least one visible character.
- **first full frame**: the first chunk after which the emulated 120x40 screen shows both the model name (`faux-1` for pig, `dummy` for phi) and the working directory's unique last path element. Both UIs print both in their chrome (pig's footer, phi's border), so the rule is the same text test for both; it needs no knowledge of how a frame is encoded and does not depend on synchronized-output markers (phi's splash animates continuously, so "quiet after the frame" cannot define the end). The time is the read time of the chunk that completed the condition; a TUI that draws chrome first and fills the transcript later is not penalised or favoured.
- **ready**: first full frame + 1.0 s (grammar loading, background goroutines and animations have started). **idle**: ready + 10 s. **at first frame** is sampled immediately.
- RSS and PSS come from `/proc/PID/smaps_rollup` (`Rss`, `Pss`, `Pss_Anon`, `Pss_File`) summed over the process and every descendant; `smaps` supplies the per-mapping split: `binary_text` (r-x of the executable), `binary_rodata` (r--, includes `.gopclntab`), `binary_data` (rw- file part), `anon:Go: heap` and the other `anon:Go: ...` mappings the runtime names (Go 1.27), `main_stack`, `kernel_pages`. Go keeps goroutine and thread stacks inside the heap arena, so stacks are not separable in `smaps`; `/memory/classes/heap/stacks` and `/memory/classes/os-stacks` from the scratch builds give them. File-backed RSS counts every mapped page of the binary including the kernel's fault-around neighbours (64 kB on xd670), so it overstates the pages the program touched; PSS does too (same pages, one mapper). `Referenced` is recorded as well but equals RSS on this kernel, so it does not separate the two. The 10-session job reports total RSS (sum, counts shared binary pages ten times) and total PSS (shares them).
- CPU time is `utime+stime` from `/proc/PID/stat` (10 ms ticks).

### Page cache

- **cold**: before each run the binary's page-cache pages are dropped with `posix_fadvise(POSIX_FADV_DONTNEED)` (verified with `fincore`, recorded as `pages_cached_before`), and `HOME`, the working directory and every application cache are new. The whole-system cache is not dropped: the host is shared and `drop_caches` would hurt other tenants. The two programs are static, so the binary is the only file they must read from disk; the Go runtime, libc and loader do not exist.
- **warm**: binary in the page cache (an untimed run first), a new `HOME` per run (application caches cold: pig stages its docs/SDK tree, phi creates its project directories).
- **warmhome**: warm binary and a `HOME` reused across runs after three untimed seeding launches (application caches warm).

`first-frame --silent` runs the same probe against a terminal that answers nothing, which shows how long each program waits for DA1 and OSC 11.

Subjects run round-robin in a fixed rotating order inside one job, so slow drift of the host affects every subject alike.

## Host discipline (xd670)

Measurement jobs run through `run.sh`, which takes `flock /tmp/perf-xd670.lock` and runs the whole probe, driver and subject, under `taskset -c 0-63`. CPUs 0-47 belong to NUMA node 0 and 48-63 to node 1; the mask spans both. The CPU governor is `powersave`, the host runs unrelated services on neighbouring CPUs (load average ~15 at the start), and no part of the 0-63 set is isolated from kernel threads. Treat the absolute milliseconds as xd670 numbers; the same-host differences between subjects are the finding. Builds and static analysis run on `taskset -c 64-95,160-191 nice -n 10`.

## Reproduce

```sh
# builds (not a measurement)
PIG_SRC=~/PiG PHI_SRC=~/phi OUT=~/phi-cmp/bin taskset -c 64-95,160-191 nice -n 10 automation/perf/phi-comparison/build.sh
# the whole first-frame / memory matrix; each cell is one locked job
export PIG_BIN=... PHI_BIN=... PIG_SCRATCH_BIN=... PHI_SCRATCH_BIN=... FLOOR_BIN=...
RESULTS=~/phi-cmp/results N=30 M=7 automation/perf/phi-comparison/run-all.sh   # or: run-all.sh first-frame|memory|concurrent|extras
# or one cell
automation/perf/phi-comparison/run.sh first-frame --subject pig,phi --mode warm --runs 30 --out ff.jsonl
automation/perf/phi-comparison/run.sh memory --subject pig --sessions 10 --runs 5 --out mem.jsonl
automation/perf/phi-comparison/run.sh summarize ff.jsonl mem.jsonl      # markdown tables: median (min-max), p90
```

Python 3 standard library only. `python3 -m unittest automation/perf/phi-comparison/test_vtscreen.py` covers the query classifier, the screen model and the answer logic.

| file | purpose |
|---|---|
| `ptyprobe.py` | pty driver: `first-frame`, `memory`, `summarize` |
| `vtscreen.py` | terminal output parser and character grid (queries, cursor, erase, scroll, wide characters) |
| `build.sh`, `run.sh`, `run-all.sh` | builds with release flags, locked job launcher, the matrix |
| `scratch/` | scratch-build additions (memory classes, grammar-load switch) |
| `floor/` | harness floor program |
| `binsize.py` | binary size by section, per strip tag, per package; embed compression figures |
| `frame/` | provider frame allocation benchmarks: PiG `BenchmarkProviderStreamFrames/anthropic-text` and the matching phi benchmark (kept as `.txt`, copied into a phi checkout at run time) |

## Limits

- One machine, one terminal size, one fake terminal. The terminal model answers two queries; a terminal that answers OSC 4 or the kitty queries would change what each program waits for.
- `first full frame` is a content test on an emulated screen, not a pixel on a display; the parser handles the sequences these two programs emit and skips what it does not know.
- Nothing here exercises a turn, a long session, extensions or a real terminal emulator's render time.

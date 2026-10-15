# sessionscale: long-lived session memory and per-prompt cost

`sessionscale` writes synthetic Pi session files with the shape of a long-lived session and measures a runtime that resumes them: resident memory after the resume, after N prompts and during a streaming soak, and the CPU and wall time of each prompt. Pi and PiG resume the same bytes, and the scripted model records a hash of each request's conversation, so the two can be checked to send the same context.

The default shape is the one reported for a five-day session (2026-10-10): 299 MB, 46,100 entries (25,171 tool results, 20,138 assistant messages, 700 user messages), 63 compactions, 17 leaves, a longest line of 1.06 MB. Only those counts and sizes are used. The content is pseudo-random from a seed.

```sh
go build -o sessionscale ./bench/sessionscale/cmd/sessionscale
go build -o pigprobe ./bench/sessionscale/cmd/pigprobe      # cmd/pig plus SIGUSR1 heap and SIGUSR2 CPU profiles
./sessionscale gen -out f299 -bytes 299M                      # 150k-token active context
./sessionscale gen -out f1g -bytes 1G
./sessionscale gen -out c1m -bytes 299M -context 1000000      # 1M-token active context (also 2000000, 4000000)
./sessionscale gen -out p1 -profile p1                        # real-session profile (handoffs P1): 314 MB, priced prompt caching with misses on two models, /tree rows about 61% of entries
./sessionscale run -arm pig -cmd "./pigprobe -ne" -fixture f299 -prompts 5,50 -heap -cpu -out runs/pig.jsonl
./sessionscale run -arm pi -cmd "node .../pi-coding-agent/dist/cli.js -ne" -fixture f299 -prompts 5,50 -out runs/pi.jsonl
./sessionscale run ... -soak 6m -soak-every 1m                 # slow streaming answers with idle gaps
```

`run` copies the session file into a scratch directory, starts the runtime in a pseudo-terminal (or `-mode rpc`) with `--session` and a scratch HOME and agent directory, and writes one JSON line per point: RSS, PSS, anonymous and private dirty memory and peak RSS of the process tree (KiB, from `/proc`), CPU and wall time, per-prompt CPU and wall time, and the model requests (bytes, message count, conversation hash). With `-heap` and `-cpu` (pigprobe only) it also writes `inuse_space` heap profiles at each point and a CPU profile of each checkpoint's prompts.

`sessionscale k8s seed` and `sessionscale k8s sample` are bench/k8s's adapter: the profiles `session-scale-context` and `session-scale-entries` run them in a Kubernetes pod (docs/site/docs/benchmarking.md). `seed` writes one fixture and `k8s-<size>.json` with its fingerprint. `sample` runs `run` once, interactive, on a copy with five prompts, and prints one k8sbench sample line.

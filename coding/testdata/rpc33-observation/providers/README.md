# Multi-provider RPC33 start-state oracle (gap-d82)

`pi-start.json` holds the first assistant `message_start` message that real Pi 0.87.1 (Node 24.19.0, `--mode rpc --no-extensions`) prints for one `READ` prompt, per API. `server.mjs` is the loopback fixture (headers and body in one write); `drive.mjs` runs Pi or PiG against it; `check.mjs` compares.

```bash
# PiG: exit 0 only when every run of every API equals Pi
node check.mjs <pig-binary> 200 [api...]
# Regenerate the Pi oracle (requires extensions/sdk-ts npm ci)
node check.mjs --oracle extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/cli.js 10
```

Keys are sorted and clocks and Google's generated IDs (`name_<ms>_<n>`) are canonicalized; nothing else is normalized. A provider lane closes its API when `check.mjs` reports `N/N equal Pi` for 200 runs.

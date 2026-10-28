### Fixed

- A tool running under the Durable core's Cloudflare host and `pi-durable` facade now has `api.now()`, the Harness clock (`HarnessOptions.now`, the wall clock when none is given), which throws once the tool call's invocation has ended, as Pi 1.4's `ToolExecutionApi.now` does. Code mode reads its start time, journaled clock and deadline through it, so every code mode call, and every `models.*` call from a script, failed with `api.now is not a function` before. <!-- fix: a9264aabbb9c032cd8d504c22059d73efd84b95d -->

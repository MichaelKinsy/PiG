# Pi 0.87.1 Anthropic E2E case snapshot

`anthropic_e2e_cases.json` is copied unchanged from `ai/testdata/anthropic_e2e_cases.json` at frozen provider-lane commit `92a8bbf91ed1ac8cd08c53353b75c6a711ae4f61`.

SHA-256: `29081cac2593b1400cc2444b9c384d22f18e8331bee54b87580fb418f5bc181e`.

The reviewed snapshot contains 347 Anthropic Messages model identities, 11 configured probes, and 10 forced-eager probes. The selector preserves the priority and JavaScript `localeCompare` rules in `packages/ai/test/anthropic-eager-tool-input-e2e.test.ts:34-85` and `packages/ai/test/anthropic-long-cache-retention-e2e.test.ts:22-70`. The test checks the complete catalog denominator independently against Pig's catalog. It does not replace a remote response with fixture data.

The owning regeneration command at that frozen ref is `node test/parity/testdata/generate-anthropic-e2e-cases.mjs > ai/testdata/anthropic_e2e_cases.json`. The compile-only wave does not run that generator. Coordinate any snapshot update with the aggregator.

Select the `live` build tag for the provider acceptance tests. The Anthropic thinking-disable and Bedrock thinking-payload cases that abort in `OnPayload` run in the default suite without credentials. Supply provider environment keys in an isolated home. Copilot uses `PIG_LIVE_COPILOT_TOKEN`, which contains the resolved token normally returned by the upstream OAuth helper. No test reads the worker's auth files. Missing credentials fail the selected live case. The selected catalog routes require Anthropic, Cloudflare AI Gateway (including account/gateway endpoint configuration), Fireworks, GitHub Copilot, Kimi Coding, MiniMax, MiniMax CN, OpenCode, OpenRouter, and Vercel AI Gateway access.

# Pi 1.1.0 Anthropic E2E case snapshot

`anthropic_e2e_cases.json` lists the Anthropic Messages models of the published pinned pi-ai catalog and the probes the upstream acceptance tests select from them.

SHA-256: `880222f059c8ba8209009b6a4b8a3628fb37cb1f704490dfc882a9420f746fbf`.

The snapshot contains 355 Anthropic Messages model identities, 11 configured probes, and 10 forced-eager probes. The selector preserves the priority and JavaScript `localeCompare` rules in `packages/ai/test/anthropic-eager-tool-input-e2e.test.ts:34-85` and `packages/ai/test/anthropic-long-cache-retention-e2e.test.ts:22-70`. The test checks the complete catalog denominator independently against Pig's catalog. It does not replace a remote response with fixture data.

The owning regeneration command is `node test/parity/testdata/generate-anthropic-e2e-cases.mjs > ai/testdata/port-wave-13/anthropic_e2e_cases.json` after `make model-catalogs` installs the pinned package. Applied to the 0.87.1 data, the same selector reproduces the previous reviewed snapshot byte for byte in content.

Select the `live` build tag for the provider acceptance tests. The Anthropic thinking-disable and Bedrock thinking-payload cases that abort in `OnPayload` run in the default suite without credentials. Supply provider environment keys in an isolated home. Copilot uses `PIG_LIVE_COPILOT_TOKEN`, which contains the resolved token normally returned by the upstream OAuth helper. No test reads the worker's auth files. Missing credentials fail the selected live case. The selected catalog routes require Anthropic, Cloudflare AI Gateway (including account/gateway endpoint configuration), Fireworks, GitHub Copilot, Kimi Coding, MiniMax, MiniMax CN, OpenCode, OpenRouter, and Vercel AI Gateway access.
